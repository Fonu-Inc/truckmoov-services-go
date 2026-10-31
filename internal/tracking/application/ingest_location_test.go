package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/domain"
)

type fakeTrackingPublisher struct {
	location domain.LocationUpdate
}

func (f *fakeTrackingPublisher) PublishLocationUpdate(_ context.Context, event domain.LocationUpdate) error {
	f.location = event
	return nil
}

func (f *fakeTrackingPublisher) PublishAlertTriggered(_ context.Context, _ domain.AlertTriggered) error {
	return nil
}

type failOncePublisher struct {
	fakeTrackingPublisher
	failures int
}

func (f *failOncePublisher) PublishLocationUpdate(ctx context.Context, event domain.LocationUpdate) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("bus down")
	}
	return f.fakeTrackingPublisher.PublishLocationUpdate(ctx, event)
}

type fakeLatestLocationRepository struct {
	entry LatestLocation
	byKey map[string]LatestLocation
}

func (f *fakeLatestLocationRepository) Save(location LatestLocation) error {
	f.entry = location
	if f.byKey == nil {
		f.byKey = make(map[string]LatestLocation)
	}
	f.byKey[location.TenantID+"|"+location.VehicleID] = location
	return nil
}

func (f *fakeLatestLocationRepository) Get(tenantID, vehicleID string) (*LatestLocation, error) {
	if f.byKey == nil {
		return nil, nil
	}
	stored, ok := f.byKey[tenantID+"|"+vehicleID]
	if !ok {
		return nil, nil
	}
	return &stored, nil
}

type fakeLocationHistoryRepository struct {
	entry LatestLocation
}

func (f *fakeLocationHistoryRepository) Append(entry LatestLocation) error {
	f.entry = entry
	return nil
}

func (f *fakeLocationHistoryRepository) History(_, _ string, _ int) ([]LatestLocation, error) {
	return nil, nil
}

type fixedTrackingClock struct{ now time.Time }

func (f fixedTrackingClock) Now() time.Time { return f.now }

type fixedTrackingIDGenerator struct{ value string }

func (f fixedTrackingIDGenerator) New(_ string) string { return f.value }

func TestIngestLocationUseCase_Execute(t *testing.T) {
	publisher := &fakeTrackingPublisher{}
	repository := &fakeLatestLocationRepository{}
	historyRepo := &fakeLocationHistoryRepository{}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-123"},
		"default",
		historyRepo,
		TelemetryConfig{},
		nil,
		nil,
	)

	result, err := useCase.Execute(context.Background(), IngestLocationCommand{
		VehicleID: "vehicle-1",
		DeviceID:  "device-1",
		Latitude:  6.5,
		Longitude: 3.4,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !result.Published {
		t.Fatalf("expected published=true")
	}
	if result.Subject != "tm.default.vehicle.location.updated" {
		t.Fatalf("unexpected subject: %s", result.Subject)
	}
	if repository.entry.VehicleID != "vehicle-1" {
		t.Fatalf("expected stored vehicle id, got %s", repository.entry.VehicleID)
	}
}

func TestIngestLocationUseCase_Execute_CarriesProtocolToPublishedEvent(t *testing.T) {
	publisher := &fakeTrackingPublisher{}
	repository := &fakeLatestLocationRepository{}
	historyRepo := &fakeLocationHistoryRepository{}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-123"},
		"default",
		historyRepo,
		TelemetryConfig{},
		nil,
		nil,
	)

	_, err := useCase.Execute(context.Background(), IngestLocationCommand{
		VehicleID: "vehicle-1",
		DeviceID:  "device-1",
		Latitude:  6.5,
		Longitude: 3.4,
		Protocol:  "teltonika",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if publisher.location.Protocol != "teltonika" {
		t.Fatalf("expected published event to carry protocol, got %q", publisher.location.Protocol)
	}
}

func TestIngestLocationUseCase_Execute_SkipsDuplicateVendorPosition(t *testing.T) {
	publisher := &fakeTrackingPublisher{}
	repository := &fakeLatestLocationRepository{}
	historyRepo := &fakeLocationHistoryRepository{}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-123"},
		"default",
		historyRepo,
		TelemetryConfig{},
		nil,
		nil,
	)

	command := IngestLocationCommand{
		VehicleID:        "vehicle-1",
		DeviceID:         "device-1",
		Latitude:         6.5,
		Longitude:        3.4,
		FixTime:          "2026-02-02T11:00:00Z",
		VendorPositionID: "traccar-pos-42",
	}

	first, err := useCase.Execute(context.Background(), command)
	if err != nil {
		t.Fatalf("expected no error on first ingest, got %v", err)
	}
	if !first.Published || first.Skipped {
		t.Fatalf("expected first delivery to publish, got %+v", first)
	}

	// Same Traccar position re-delivered (e.g. a webhook retry after a timeout).
	second, err := useCase.Execute(context.Background(), command)
	if err != nil {
		t.Fatalf("expected no error on retried ingest, got %v", err)
	}
	if !second.Skipped || second.SkipReason != "duplicate_vendor_position" {
		t.Fatalf("expected duplicate_vendor_position skip, got %+v", second)
	}
	if second.Published {
		t.Fatalf("expected retried delivery not to be published")
	}
}

func TestIngestLocationUseCase_Execute_PersistsWhenPublishFailsAsync(t *testing.T) {
	publisher := &failOncePublisher{failures: 100}
	repository := &fakeLatestLocationRepository{}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-1"},
		"default",
		&fakeLocationHistoryRepository{},
		TelemetryConfig{},
		nil,
		nil,
	)
	command := IngestLocationCommand{
		VehicleID:        "vehicle-1",
		DeviceID:         "device-1",
		Latitude:         6.5,
		Longitude:        3.4,
		FixTime:          "2026-02-02T11:00:00Z",
		VendorPositionID: "traccar-pos-7",
	}

	result, err := useCase.Execute(context.Background(), command)
	if err != nil || !result.Published {
		t.Fatalf("live path must succeed after Redis save, got %+v err=%v", result, err)
	}
	if len(repository.byKey) != 1 {
		t.Fatalf("snapshot must be stored even if NATS publish fails async")
	}
	// Let the failing goroutine finish so the test process does not leak it.
	time.Sleep(20 * time.Millisecond)
}

type failingHistoryRepository struct{ fakeLocationHistoryRepository }

func (*failingHistoryRepository) Append(LatestLocation) error { return errors.New("redis down") }

func TestIngestLocation_HistoryFailureDoesNotFailIngest(t *testing.T) {
	publisher := &fakeTrackingPublisher{}
	useCase := NewIngestLocationUseCase(
		publisher,
		&fakeLatestLocationRepository{},
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-1"},
		"default",
		&failingHistoryRepository{},
		TelemetryConfig{},
		nil,
		nil,
	)

	result, err := useCase.Execute(context.Background(), IngestLocationCommand{
		VehicleID: "vehicle-1", DeviceID: "device-1", Latitude: 6.5, Longitude: 3.4,
	})
	if err != nil || !result.Published {
		t.Fatalf("expected a published fix despite history failure, got %#v, %v", result, err)
	}
}
