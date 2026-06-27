package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/logger"
)

type fakeIMEIResolver struct {
	entries map[string]DeviceIdentity
}

func newFakeIMEIResolver() *fakeIMEIResolver {
	return &fakeIMEIResolver{entries: make(map[string]DeviceIdentity)}
}

func (f *fakeIMEIResolver) Resolve(imei string) (*DeviceIdentity, error) {
	if entry, ok := f.entries[imei]; ok {
		return &entry, nil
	}
	return nil, nil
}

func (f *fakeIMEIResolver) Upsert(identity DeviceIdentity) error {
	f.entries[identity.IMEI] = identity
	return nil
}

func (f *fakeIMEIResolver) Evict(imei string) error {
	delete(f.entries, imei)
	return nil
}

func TestIngestLocation_ResolvesIMEIToVehicle(t *testing.T) {
	resolver := newFakeIMEIResolver()
	_ = resolver.Upsert(DeviceIdentity{
		IMEI:        "359779090012345",
		TenantID:    "tenant-9",
		VehicleUUID: "vehicle-uuid-9",
		DeviceID:    "device-uuid-9",
	})

	publisher := &fakeTrackingPublisher{}
	repository := &fakeLatestLocationRepository{}
	historyRepo := &fakeLocationHistoryRepository{}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-1"},
		"default",
		historyRepo,
		TelemetryConfig{},
		resolver,
		nil,
	)

	_, err := useCase.Execute(context.Background(), IngestLocationCommand{
		IMEI:      "359779090012345",
		Latitude:  6.5,
		Longitude: 3.4,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if repository.entry.VehicleID != "vehicle-uuid-9" {
		t.Fatalf("expected vehicle uuid keyed, got %s", repository.entry.VehicleID)
	}
	if repository.entry.DeviceID != "device-uuid-9" {
		t.Fatalf("expected device uuid keyed, got %s", repository.entry.DeviceID)
	}
	if repository.entry.TenantID != "tenant-9" {
		t.Fatalf("expected resolved tenant, got %s", repository.entry.TenantID)
	}
}

func TestIngestLocation_UnresolvedIMEIFallsBack(t *testing.T) {
	publisher := &fakeTrackingPublisher{}
	repository := &fakeLatestLocationRepository{}
	historyRepo := &fakeLocationHistoryRepository{}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-1"},
		"default",
		historyRepo,
		TelemetryConfig{},
		newFakeIMEIResolver(),
		nil,
	)

	_, err := useCase.Execute(context.Background(), IngestLocationCommand{
		IMEI:      "359779090099999",
		Latitude:  6.5,
		Longitude: 3.4,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if repository.entry.VehicleID != "359779090099999" || repository.entry.DeviceID != "359779090099999" {
		t.Fatalf("expected IMEI fallback keying, got vehicle=%s device=%s", repository.entry.VehicleID, repository.entry.DeviceID)
	}
}

func TestIngestLocation_AccuracyFilteredDespiteUnparseableFixTime(t *testing.T) {
	publisher := &fakeTrackingPublisher{}
	repository := &fakeLatestLocationRepository{}
	historyRepo := &fakeLocationHistoryRepository{}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-1"},
		"default",
		historyRepo,
		TelemetryConfig{MaxAccuracyM: 100},
		nil,
		nil,
	)

	bad := 500.0
	result, err := useCase.Execute(context.Background(), IngestLocationCommand{
		VehicleID: "vehicle-1",
		DeviceID:  "device-1",
		Latitude:  6.5,
		Longitude: 3.4,
		AccuracyM: &bad,
		FixTime:   "not-a-timestamp",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !result.Skipped || result.SkipReason != "accuracy_exceeded" {
		t.Fatalf("expected accuracy_exceeded skip even with unparseable fix_time, got %#v", result)
	}
}

func TestConsumeDeviceIdentity_UpsertAndEvict(t *testing.T) {
	resolver := newFakeIMEIResolver()
	useCase := NewConsumeDeviceIdentityUseCase(resolver, logger.New("test"))

	upsert := eventbus.EventEnvelope{
		TenantID: "tenant-3",
		Data: map[string]any{
			"imei":       "359779090011111",
			"device_id":  "device-uuid-3",
			"vehicle_id": "vehicle-uuid-3",
			"action":     "assigned",
		},
	}
	if err := useCase.Handle(context.Background(), upsert); err != nil {
		t.Fatalf("upsert handle error: %v", err)
	}
	got, _ := resolver.Resolve("359779090011111")
	if got == nil || got.VehicleUUID != "vehicle-uuid-3" {
		t.Fatalf("expected cached vehicle mapping, got %#v", got)
	}

	evict := eventbus.EventEnvelope{
		TenantID: "tenant-3",
		Data: map[string]any{
			"imei":   "359779090011111",
			"action": "decommissioned",
		},
	}
	if err := useCase.Handle(context.Background(), evict); err != nil {
		t.Fatalf("evict handle error: %v", err)
	}
	if got, _ := resolver.Resolve("359779090011111"); got != nil {
		t.Fatalf("expected eviction, still cached: %#v", got)
	}
}
