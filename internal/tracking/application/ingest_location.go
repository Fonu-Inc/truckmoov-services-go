package application

import (
	"context"
	"fmt"
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
	Protocol        string
	FixTime         string
	// VendorPositionID, when set by a vendor adapter (e.g. Traccar's
	// position.id), lets Execute detect and skip a re-delivered fix — a
	// forwarding webhook retry after a timeout or non-2xx response — without
	// depending solely on fix-time ordering, which a malformed/missing
	// fix_time would otherwise bypass entirely.
	VendorPositionID string
}

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

// resolveIdentity rekeys an IMEI-bearing command onto the canonical fleet
// tenant/vehicle/device. When the IMEI is unknown (resolver miss or cache cold)
// it falls back to IMEI keying so telemetry still flows — fixing the prior bug
// where the IMEI was stuffed into both vehicle_id and device_id unconditionally.
func (u *IngestLocationUseCase) resolveIdentity(command IngestLocationCommand) (tenantID, vehicleID, deviceID string) {
	tenantID = command.TenantID
	vehicleID = command.VehicleID
	deviceID = command.DeviceID

	if command.IMEI != "" {
		deviceID = command.IMEI
		vehicleID = command.IMEI
		if u.resolver != nil {
			if identity, err := u.resolver.Resolve(command.IMEI); err == nil && identity != nil {
				if identity.TenantID != "" {
					tenantID = identity.TenantID
				}
				if identity.DeviceID != "" {
					deviceID = identity.DeviceID
				}
				if identity.VehicleUUID != "" {
					vehicleID = identity.VehicleUUID
				}
			}
		}
	}

	if tenantID == "" {
		tenantID = u.defaultTenant
	}
	return tenantID, vehicleID, deviceID
}

func (u *IngestLocationUseCase) Execute(
	ctx context.Context,
	command IngestLocationCommand,
) (IngestLocationResult, error) {
	tenantID, vehicleID, deviceID := u.resolveIdentity(command)

	fixTime := command.FixTime
	if fixTime == "" {
		fixTime = u.clock.Now().UTC().Format(time.RFC3339)
	}

	parsedFix, fixErr := time.Parse(time.RFC3339, fixTime)
	prev, _ := u.locationStore.Get(tenantID, vehicleID)

	if command.VendorPositionID != "" && prev != nil && prev.VendorPositionID == command.VendorPositionID {
		return IngestLocationResult{Skipped: true, SkipReason: "duplicate_vendor_position"}, nil
	}

	if r := applyTelemetryFilter(u.telemetry, prev, command, parsedFix, fixErr == nil); r.skip {
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
	})
	if err != nil {
		return IngestLocationResult{}, fmt.Errorf("build location event: %w", err)
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
		return IngestLocationResult{}, fmt.Errorf("save latest location: %w", err)
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
		return IngestLocationResult{}, fmt.Errorf("append location history: %w", err)
	}

	// Time-series persistence is best-effort: the live path (Redis snapshot + NATS
	// event) is the source of truth, so a Timescale outage must not fail ingest.
	if u.telemetryStore != nil {
		var fixTimePtr *time.Time
		if fixErr == nil {
			f := parsedFix
			fixTimePtr = &f
		}
		_ = u.telemetryStore.SaveSample(ctx, TelemetrySample{
			TenantID:        locationUpdate.TenantID,
			DeviceID:        locationUpdate.DeviceID,
			VehicleID:       locationUpdate.VehicleID,
			IMEI:            command.IMEI,
			Latitude:        locationUpdate.Latitude,
			Longitude:       locationUpdate.Longitude,
			SpeedKPH:        locationUpdate.SpeedKPH,
			Heading:         locationUpdate.Heading,
			AccuracyM:       locationUpdate.AccuracyM,
			BatteryLevel:    command.BatteryLevel,
			SignalStrength:  command.SignalStrength,
			FuelLevel:       command.FuelLevel,
			EngineOn:        command.EngineOn,
			FirmwareVersion: command.FirmwareVersion,
			Protocol:        command.Protocol,
			FixTime:         fixTimePtr,
			ObservedAt:      locationUpdate.OccurredAt,
			EventID:         locationUpdate.EventID,
		})
	}

	if err := u.publisher.PublishLocationUpdate(ctx, locationUpdate); err != nil {
		return IngestLocationResult{}, fmt.Errorf("publish location event: %w", err)
	}

	return IngestLocationResult{
		Published: true,
		Subject:   locationUpdate.Subject(),
		EventID:   locationUpdate.EventID,
	}, nil
}
