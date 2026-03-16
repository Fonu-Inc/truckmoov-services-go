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
	Latitude  float64
	Longitude float64
	SpeedKPH  *float64
	Heading   *float64
	AccuracyM *float64
	FixTime   string
}

type IngestLocationResult struct {
	Published  bool   `json:"published"`
	Subject    string `json:"subject,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skip_reason,omitempty"`
}

type IngestLocationUseCase struct {
	publisher     TrackingEventPublisher
	locationStore LatestLocationRepository
	clock         Clock
	idGenerator   IDGenerator
	defaultTenant string
	historyStore  LocationHistoryRepository
	telemetry     TelemetryConfig
}

func NewIngestLocationUseCase(
	publisher TrackingEventPublisher,
	locationStore LatestLocationRepository,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
	historyStore LocationHistoryRepository,
	telemetry TelemetryConfig,
) *IngestLocationUseCase {
	return &IngestLocationUseCase{
		publisher:     publisher,
		locationStore: locationStore,
		clock:         clock,
		idGenerator:   idGenerator,
		defaultTenant: defaultTenant,
		historyStore:  historyStore,
		telemetry:     telemetry,
	}
}

func (u *IngestLocationUseCase) Execute(
	ctx context.Context,
	command IngestLocationCommand,
) (IngestLocationResult, error) {
	tenantID := command.TenantID
	if tenantID == "" {
		tenantID = u.defaultTenant
	}

	fixTime := command.FixTime
	if fixTime == "" {
		fixTime = u.clock.Now().UTC().Format(time.RFC3339)
	}

	if parsedFix, err := time.Parse(time.RFC3339, fixTime); err == nil {
		prev, _ := u.locationStore.Get(tenantID, command.VehicleID)
		if r := applyTelemetryFilter(u.telemetry, prev, command, parsedFix); r.skip {
			return IngestLocationResult{Skipped: true, SkipReason: r.reason}, nil
		}
	}

	locationUpdate, err := domain.NewLocationUpdate(domain.LocationUpdate{
		EventID:    u.idGenerator.New("evt"),
		OccurredAt: u.clock.Now(),
		TenantID:   tenantID,
		VehicleID:  command.VehicleID,
		DeviceID:   command.DeviceID,
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

	if err := u.publisher.PublishLocationUpdate(ctx, locationUpdate); err != nil {
		return IngestLocationResult{}, fmt.Errorf("publish location event: %w", err)
	}

	return IngestLocationResult{
		Published: true,
		Subject:   locationUpdate.Subject(),
		EventID:   locationUpdate.EventID,
	}, nil
}
