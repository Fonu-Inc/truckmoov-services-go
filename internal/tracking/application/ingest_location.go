package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/domain"
)

type IngestLocationCommand struct {
	TenantID  string
	VehicleID string
	DeviceID  string
	// IMEI, when set by a hardware adapter (Teltonika/Traccar), is resolved to the
	// canonical tenant/vehicle/device via the IMEI resolver before persistence.
	IMEI      string
	Latitude  float64
	Longitude float64
	SpeedKPH  *float64
	Heading   *float64
	AccuracyM *float64
	// Optional non-GPS telemetry carried into the time-series store (nil when the
	// vendor payload omits the signal).
	BatteryLevel    *float64
	SignalStrength  *float64
	FuelLevel       *float64
	EngineOn        *bool
	FirmwareVersion string
	BatteryVoltage  *float64
	PowerVoltage    *float64
	OdometerM       *float64
	ICCID           string
	Alarm           string
	// NoFix marks a report without a valid GPS fix (e.g. Traccar valid:false):
	// telemetry is kept but the vehicle's location is not moved.
	NoFix    bool
	Protocol string
	FixTime  string
	// VendorPositionID, when set by a vendor adapter (e.g. Traccar's
	// position.id), lets Execute detect and skip a re-delivered fix — a
	// forwarding webhook retry after a timeout or non-2xx response — without
	// depending solely on fix-time ordering, which a malformed/missing
	// fix_time would otherwise bypass entirely.
	VendorPositionID string
}

var (
	ErrIdentityUnavailable = errors.New("device identity lookup unavailable")
	ErrIngestUnavailable   = errors.New("location storage unavailable")
	ErrInvalidFixTime      = errors.New("fix_time must be RFC3339")
)

type IngestLocationResult struct {
	Published  bool   `json:"published"`
	Subject    string `json:"subject,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skip_reason,omitempty"`
}

type IngestLocationUseCase struct {
	publisher      TrackingEventPublisher
	locationStore  LatestLocationRepository
	clock          Clock
	idGenerator    IDGenerator
	defaultTenant  string
	historyStore   LocationHistoryRepository
	telemetry      TelemetryConfig
	resolver       IMEIResolver
	telemetryStore TelemetryStore
	gate           *IngestGate
	rate           *MessageRate
	quarantine     *Quarantine
	warmup         *Warmup
	idMetrics      IdentityMetrics
	roadMatch      *RoadMatchService
	// Pod-local last fix times skip a Redis GET for out-of-order checks under flood.
	lastFix sync.Map // key tenant|vehicle -> time.Time
}

func NewIngestLocationUseCase(
	publisher TrackingEventPublisher,
	locationStore LatestLocationRepository,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
	historyStore LocationHistoryRepository,
	telemetry TelemetryConfig,
	resolver IMEIResolver,
	telemetryStore TelemetryStore,
) *IngestLocationUseCase {
	return &IngestLocationUseCase{
		publisher:      publisher,
		locationStore:  locationStore,
		clock:          clock,
		idGenerator:    idGenerator,
		defaultTenant:  defaultTenant,
		historyStore:   historyStore,
		telemetry:      telemetry,
		resolver:       resolver,
		telemetryStore: telemetryStore,
	}
}

func (u *IngestLocationUseCase) WithGate(gate *IngestGate) *IngestLocationUseCase {
	u.gate = gate
	return u
}

func (u *IngestLocationUseCase) WithMessageRate(rate *MessageRate) *IngestLocationUseCase {
	u.rate = rate
	return u
}

func (u *IngestLocationUseCase) WithQuarantine(quarantine *Quarantine) *IngestLocationUseCase {
	u.quarantine = quarantine
	return u
}

func (u *IngestLocationUseCase) WithWarmup(warmup *Warmup) *IngestLocationUseCase {
	u.warmup = warmup
	return u
}

func (u *IngestLocationUseCase) WithIdentityMetrics(metrics IdentityMetrics) *IngestLocationUseCase {
	u.idMetrics = metrics
	return u
}

func (u *IngestLocationUseCase) WithRoadMatching(roadMatch *RoadMatchService) *IngestLocationUseCase {
	u.roadMatch = roadMatch
	return u
}

type resolvedIdentity struct {
	tenantID  string
	vehicleID string
	deviceID  string
	known     *DeviceIdentity
	lookupErr error
}

// resolveIdentity rekeys an IMEI-bearing command onto the canonical fleet
// tenant/vehicle/device. An IMEI the resolver does not know stays unresolved
// (known == nil) and Execute quarantines it.
func (u *IngestLocationUseCase) resolveIdentity(command IngestLocationCommand) resolvedIdentity {
	resolved := resolvedIdentity{tenantID: command.TenantID, vehicleID: command.VehicleID, deviceID: command.DeviceID}

	if command.IMEI != "" {
		resolved.deviceID = command.IMEI
		resolved.vehicleID = command.IMEI
		if u.resolver != nil {
			identity, err := u.resolver.Resolve(command.IMEI)
			resolved.lookupErr = err
			if err == nil && identity != nil {
				resolved.known = identity
				resolved.tenantID = identity.TenantID
				if identity.DeviceID != "" {
					resolved.deviceID = identity.DeviceID
				}
				if identity.VehicleUUID != "" {
					resolved.vehicleID = identity.VehicleUUID
				}
			}
		}
	}

	if resolved.tenantID == "" {
		resolved.tenantID = u.defaultTenant
	}
	return resolved
}

func (u *IngestLocationUseCase) Execute(
	ctx context.Context,
	command IngestLocationCommand,
) (IngestLocationResult, error) {
	if !u.warmup.Ready() {
		return IngestLocationResult{}, ErrWarmingUp
	}
	resolved := u.resolveIdentity(command)
	if resolved.lookupErr != nil {
		return IngestLocationResult{}, fmt.Errorf("%w: %v", ErrIdentityUnavailable, resolved.lookupErr)
	}
	if skipped, reason := u.screen(ctx, command, resolved); skipped {
		return IngestLocationResult{Skipped: true, SkipReason: reason}, nil
	}
	tenantID, vehicleID, deviceID := resolved.tenantID, resolved.vehicleID, resolved.deviceID

	fixTime := command.FixTime
	if fixTime == "" {
		fixTime = u.clock.Now().UTC().Format(time.RFC3339)
	}

	parsedFix, err := time.Parse(time.RFC3339, fixTime)
	if err != nil {
		return IngestLocationResult{}, ErrInvalidFixTime
	}

	if command.NoFix {
		u.saveSample(ctx, command, tenantID, vehicleID, deviceID, parsedFix, u.clock.Now(), "")
		return IngestLocationResult{Skipped: true, SkipReason: "no_gps_fix"}, nil
	}

	lastKey := tenantID + "|" + vehicleID
	needPrev := command.VendorPositionID != "" || u.telemetry.MaxSpeedKPH > 0
	var prev *LatestLocation
	if needPrev {
		prev, _ = u.locationStore.Get(tenantID, vehicleID)
	} else if last, ok := u.lastFix.Load(lastKey); ok {
		// Hot path: skip Redis GET; pod-local stamp catches obvious OOO under flood.
		if lastTime, ok := last.(time.Time); ok && !parsedFix.After(lastTime) {
			return IngestLocationResult{Skipped: true, SkipReason: "out_of_order"}, nil
		}
	}

	if command.VendorPositionID != "" && prev != nil && prev.VendorPositionID == command.VendorPositionID {
		return IngestLocationResult{Skipped: true, SkipReason: "duplicate_vendor_position"}, nil
	}

	if r := applyTelemetryFilter(u.telemetry, prev, command, parsedFix); r.skip {
		slog.Debug("ingest_filtered", "reason", r.reason, "tenant", tenantID, "device", deviceID)
		return IngestLocationResult{Skipped: true, SkipReason: r.reason}, nil
	}

	locationUpdate, err := domain.NewLocationUpdate(domain.LocationUpdate{
		EventID:    u.idGenerator.New("evt"),
		OccurredAt: u.clock.Now(),
		TenantID:   tenantID,
		VehicleID:  vehicleID,
		DeviceID:   deviceID,
		IMEI:       command.IMEI,
		Latitude:   command.Latitude,
		Longitude:  command.Longitude,
		SpeedKPH:   command.SpeedKPH,
		Heading:    command.Heading,
		AccuracyM:  command.AccuracyM,
		FixTimeRFC: fixTime,
		Protocol:   command.Protocol,
	})
	if err != nil {
		return IngestLocationResult{}, fmt.Errorf("build location event: %w", err)
	}

	// Start JetStream publish first, then overlap Redis snapshot with the PubAck.
	// Still wait for ack before success so a bus failure returns 503 and vendors retry.
	ack, err := u.beginPublish(ctx, locationUpdate)
	if err != nil {
		return IngestLocationResult{}, fmt.Errorf("publish location event: %w: %w", ErrIngestUnavailable, err)
	}

	if err := u.locationStore.Save(LatestLocation{
		TenantID:         locationUpdate.TenantID,
		VehicleID:        locationUpdate.VehicleID,
		DeviceID:         locationUpdate.DeviceID,
		Latitude:         locationUpdate.Latitude,
		Longitude:        locationUpdate.Longitude,
		SpeedKPH:         locationUpdate.SpeedKPH,
		Heading:          locationUpdate.Heading,
		AccuracyM:        locationUpdate.AccuracyM,
		FixTimeRFC:       locationUpdate.FixTimeRFC,
		ObservedAt:       locationUpdate.OccurredAt,
		EventID:          locationUpdate.EventID,
		VendorPositionID: command.VendorPositionID,
	}); err != nil {
		_ = ack()
		return IngestLocationResult{}, fmt.Errorf("save latest location: %w: %w", ErrIngestUnavailable, err)
	}

	if err := u.historyStore.Append(LatestLocation{
		TenantID:   locationUpdate.TenantID,
		VehicleID:  locationUpdate.VehicleID,
		DeviceID:   locationUpdate.DeviceID,
		Latitude:   locationUpdate.Latitude,
		Longitude:  locationUpdate.Longitude,
		SpeedKPH:   locationUpdate.SpeedKPH,
		Heading:    locationUpdate.Heading,
		AccuracyM:  locationUpdate.AccuracyM,
		FixTimeRFC: locationUpdate.FixTimeRFC,
		ObservedAt: locationUpdate.OccurredAt,
		EventID:    locationUpdate.EventID,
	}); err != nil {
		slog.Warn("location_history_append_failed", "tenant", tenantID, "vehicle", vehicleID, "error", err)
	}

	if err := ack(); err != nil {
		return IngestLocationResult{}, fmt.Errorf("publish location event: %w: %w", ErrIngestUnavailable, err)
	}
	u.lastFix.Store(lastKey, parsedFix)

	u.saveSample(ctx, command, tenantID, vehicleID, deviceID, parsedFix, locationUpdate.OccurredAt, locationUpdate.EventID)

	u.rate.Record(u.clock.Now())
	if u.roadMatch != nil {
		u.roadMatch.Submit(locationUpdate)
	}

	return IngestLocationResult{
		Published: true,
		Subject:   locationUpdate.Subject(),
		EventID:   locationUpdate.EventID,
	}, nil
}

// saveSample is best-effort: the live path (Redis snapshot + NATS event) is the
// source of truth, so a Timescale outage must not fail ingest. The wired store
// is async; this call returns immediately.
func (u *IngestLocationUseCase) saveSample(ctx context.Context, command IngestLocationCommand, tenantID, vehicleID, deviceID string, fix, observed time.Time, eventID string) {
	if u.telemetryStore == nil {
		return
	}
	_ = u.telemetryStore.SaveSample(ctx, TelemetrySample{
		TenantID:        tenantID,
		DeviceID:        deviceID,
		VehicleID:       vehicleID,
		IMEI:            command.IMEI,
		Latitude:        command.Latitude,
		Longitude:       command.Longitude,
		SpeedKPH:        command.SpeedKPH,
		Heading:         command.Heading,
		AccuracyM:       command.AccuracyM,
		BatteryLevel:    command.BatteryLevel,
		SignalStrength:  command.SignalStrength,
		FuelLevel:       command.FuelLevel,
		EngineOn:        command.EngineOn,
		FirmwareVersion: command.FirmwareVersion,
		BatteryVoltage:  command.BatteryVoltage,
		PowerVoltage:    command.PowerVoltage,
		OdometerM:       command.OdometerM,
		ICCID:           command.ICCID,
		Alarm:           command.Alarm,
		NoFix:           command.NoFix,
		Protocol:        command.Protocol,
		FixTime:         &fix,
		ObservedAt:      observed,
		EventID:         eventID,
	})
}

// locationUpdateBeginner starts a publish and returns an ack wait so Redis I/O
// can overlap the JetStream PubAck. Optional — fakes only implement Publish.
type locationUpdateBeginner interface {
	BeginLocationUpdate(ctx context.Context, event domain.LocationUpdate) (ack func() error, err error)
}

func (u *IngestLocationUseCase) beginPublish(ctx context.Context, event domain.LocationUpdate) (func() error, error) {
	if beginner, ok := u.publisher.(locationUpdateBeginner); ok {
		return beginner.BeginLocationUpdate(ctx, event)
	}
	if err := u.publisher.PublishLocationUpdate(ctx, event); err != nil {
		return nil, err
	}
	return func() error { return nil }, nil
}

// ResolveDevice answers who owns an IMEI through the same resolver as ingest.
// A nil identity with no error means nobody registered it.
func (u *IngestLocationUseCase) ResolveDevice(imei string) (*DeviceIdentity, error) {
	if u.resolver == nil {
		return nil, nil
	}
	identity, err := u.resolver.Resolve(imei)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdentityUnavailable, err)
	}
	return identity, nil
}

// screen decides whether a message is dropped before anything is stored or
// published. An IMEI nobody registered is quarantined, never accepted.
func (u *IngestLocationUseCase) screen(ctx context.Context, command IngestLocationCommand, resolved resolvedIdentity) (bool, string) {
	reason := u.gate.Evaluate(resolved.tenantID, resolved.known, command.IMEI)
	unknown := command.IMEI != "" && resolved.known == nil
	if unknown && (reason == "" || reason == SkipUnknownDevice) {
		if u.quarantine.IsBlocked(command.IMEI) {
			return true, SkipBlocked
		}
		u.quarantine.Observe(ctx, command.IMEI)
		if u.idMetrics != nil {
			u.idMetrics.Quarantined()
		}
		return true, SkipUnknownDevice
	}
	return reason != "", reason
}
