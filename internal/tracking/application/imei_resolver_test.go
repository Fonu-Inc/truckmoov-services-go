package application

import (
	"context"
	"errors"
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

type fakeQuarantineStore struct {
	counts  map[string]int
	fail    bool
	blocked []string
}

func (f *fakeQuarantineStore) Record(_ context.Context, imei string, count int, _ time.Time) error {
	if f.fail {
		return errors.New("store down")
	}
	if f.counts == nil {
		f.counts = map[string]int{}
	}
	f.counts[imei] += count
	return nil
}

func (f *fakeQuarantineStore) List(_ context.Context, _ int) ([]QuarantinedDevice, error) {
	out := []QuarantinedDevice{}
	for imei, count := range f.counts {
		out = append(out, QuarantinedDevice{IMEI: imei, MessageCount: count})
	}
	return out, nil
}

func (f *fakeQuarantineStore) Remove(_ context.Context, imei string) error {
	delete(f.counts, imei)
	return nil
}

func (f *fakeQuarantineStore) SetStatus(_ context.Context, imei, _ string, _ time.Time) error {
	if _, ok := f.counts[imei]; !ok {
		return ErrQuarantineNotFound
	}
	return nil
}

func (f *fakeQuarantineStore) BlockedIMEIs(_ context.Context) ([]string, error) {
	return f.blocked, nil
}

func (f *fakeQuarantineStore) Prune(_ context.Context, _ time.Time) (int, error) { return 0, nil }

func TestIngestLocation_UnknownIMEIIsQuarantinedNotStored(t *testing.T) {
	publisher := &fakeTrackingPublisher{}
	repository := &fakeLatestLocationRepository{}
	store := &fakeQuarantineStore{}
	clock := fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		clock,
		fixedTrackingIDGenerator{value: "evt-1"},
		"default",
		&fakeLocationHistoryRepository{},
		TelemetryConfig{},
		newFakeIMEIResolver(),
		nil,
	).WithQuarantine(NewQuarantine(store, clock))

	result, err := useCase.Execute(context.Background(), IngestLocationCommand{
		IMEI:      "359779090099999",
		Latitude:  6.5,
		Longitude: 3.4,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !result.Skipped || result.SkipReason != SkipUnknownDevice || result.Published {
		t.Fatalf("expected unknown_device skip, got %+v", result)
	}
	if len(repository.byKey) != 0 || publisher.location.EventID != "" {
		t.Fatalf("unknown IMEI must not be stored or published")
	}
	if store.counts["359779090099999"] != 1 {
		t.Fatalf("expected quarantine record, got %v", store.counts)
	}
}

func TestQuarantine_BatchesWritesAndRetriesAfterFailure(t *testing.T) {
	now := time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)
	store := &fakeQuarantineStore{}
	quarantine := NewQuarantine(store, tickClock{now: &now})
	ctx := context.Background()

	quarantine.Observe(ctx, "imei-1")
	quarantine.Observe(ctx, "imei-1")
	quarantine.Observe(ctx, "imei-1")
	if store.counts["imei-1"] != 1 {
		t.Fatalf("expected one write inside the flush window, got %d", store.counts["imei-1"])
	}
	now = now.Add(quarantineFlushEvery)
	quarantine.Observe(ctx, "imei-1")
	if store.counts["imei-1"] != 4 {
		t.Fatalf("expected batched count 4, got %d", store.counts["imei-1"])
	}

	store.fail = true
	now = now.Add(quarantineFlushEvery)
	quarantine.Observe(ctx, "imei-1")
	store.fail = false
	quarantine.Observe(ctx, "imei-1")
	if store.counts["imei-1"] != 6 {
		t.Fatalf("expected failed write to be carried forward, got %d", store.counts["imei-1"])
	}
}

func TestIngestLocation_ResolverOutageIsNotQuarantine(t *testing.T) {
	store := &fakeQuarantineStore{}
	clock := fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)}
	useCase := NewIngestLocationUseCase(
		&fakeTrackingPublisher{},
		&fakeLatestLocationRepository{},
		clock,
		fixedTrackingIDGenerator{value: "evt-1"},
		"default",
		&fakeLocationHistoryRepository{},
		TelemetryConfig{},
		failingIMEIResolver{},
		nil,
	).WithQuarantine(NewQuarantine(store, clock))

	_, err := useCase.Execute(context.Background(), IngestLocationCommand{IMEI: "359779090099999", Latitude: 6.5, Longitude: 3.4})
	if !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("expected ErrIdentityUnavailable, got %v", err)
	}
	if len(store.counts) != 0 {
		t.Fatalf("outage must not quarantine: %v", store.counts)
	}
}

type failingIMEIResolver struct{}

func (failingIMEIResolver) Resolve(string) (*DeviceIdentity, error) {
	return nil, errors.New("redis down")
}
func (failingIMEIResolver) Upsert(DeviceIdentity) error { return nil }
func (failingIMEIResolver) Evict(string) error          { return nil }

type tickClock struct{ now *time.Time }

func (c tickClock) Now() time.Time { return *c.now }

func TestConsumeDeviceIdentity_KeepsFirstTenantUntilItReleases(t *testing.T) {
	resolver := newFakeIMEIResolver()
	useCase := NewConsumeDeviceIdentityUseCase(resolver, logger.New("test"))
	event := func(tenant, action, device string) eventbus.EventEnvelope {
		return eventbus.EventEnvelope{TenantID: tenant, Data: map[string]any{"imei": "359779090011111", "device_id": device, "action": action}}
	}
	ctx := context.Background()

	_ = useCase.Handle(ctx, event("tenant-a", "registered", "dev-a"))
	_ = useCase.Handle(ctx, event("tenant-b", "registered", "dev-b"))
	if got, _ := resolver.Resolve("359779090011111"); got == nil || got.TenantID != "tenant-a" {
		t.Fatalf("first tenant must keep the IMEI, got %#v", got)
	}

	_ = useCase.Handle(ctx, event("tenant-b", "deleted", "dev-b"))
	if got, _ := resolver.Resolve("359779090011111"); got == nil || got.TenantID != "tenant-a" {
		t.Fatalf("another tenant must not clear the cache, got %#v", got)
	}

	_ = useCase.Handle(ctx, event("tenant-a", "decommissioned", "dev-a"))
	if got, _ := resolver.Resolve("359779090011111"); got != nil {
		t.Fatalf("owner decommission must clear the cache, got %#v", got)
	}
	_ = useCase.Handle(ctx, event("tenant-b", "registered", "dev-b"))
	if got, _ := resolver.Resolve("359779090011111"); got == nil || got.TenantID != "tenant-b" {
		t.Fatalf("expected tenant-b after release, got %#v", got)
	}
}

func TestConsumeDeviceIdentity_RegistrationClearsQuarantine(t *testing.T) {
	store := &fakeQuarantineStore{counts: map[string]int{"359779090011111": 3}}
	clock := fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)}
	useCase := NewConsumeDeviceIdentityUseCase(newFakeIMEIResolver(), logger.New("test")).WithQuarantine(NewQuarantine(store, clock))

	err := useCase.Handle(context.Background(), eventbus.EventEnvelope{
		TenantID: "tenant-a",
		Data:     map[string]any{"imei": "359779090011111", "device_id": "dev-a", "action": "registered"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.counts) != 0 {
		t.Fatalf("expected quarantine cleared, got %v", store.counts)
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
