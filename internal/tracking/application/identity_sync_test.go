package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/pkg/logger"
)

type fakeIdentitySource struct {
	pages   []IdentityPage
	known   map[string]DeviceIdentity
	err     error
	pageErr error
	lookups int
}

func (f *fakeIdentitySource) Page(_ context.Context, cursor string, _ int) (IdentityPage, error) {
	if f.pageErr != nil {
		return IdentityPage{}, f.pageErr
	}
	index := 0
	if cursor != "" {
		for i, page := range f.pages {
			if page.NextCursor == cursor {
				index = i + 1
			}
		}
	}
	return f.pages[index], nil
}

func (f *fakeIdentitySource) Lookup(_ context.Context, imei string) (*DeviceIdentity, error) {
	f.lookups++
	if f.err != nil {
		return nil, f.err
	}
	if identity, ok := f.known[imei]; ok {
		return &identity, nil
	}
	return nil, nil
}

type fakeIdentityMetrics struct {
	size        int
	results     map[string]int
	conflicts   int
	quarantined int
}

func (f *fakeIdentityMetrics) SnapshotSize(n int)        { f.size = n }
func (f *fakeIdentityMetrics) ReadThrough(result string) { f.results[result]++ }
func (f *fakeIdentityMetrics) Conflict()                 { f.conflicts++ }
func (f *fakeIdentityMetrics) Quarantined()              { f.quarantined++ }

type identityHarness struct {
	source  *fakeIdentitySource
	cache   *fakeIMEIResolver
	metrics *fakeIdentityMetrics
	clock   *gateClock
	gate    *IngestGate
	warmup  *Warmup
	sync    *IdentitySync
	reader  *ReadThroughResolver
	ingest  *IngestLocationUseCase
}

func newIdentityHarness() *identityHarness {
	log := logger.New("test")
	h := &identityHarness{
		source:  &fakeIdentitySource{known: map[string]DeviceIdentity{}},
		cache:   newFakeIMEIResolver(),
		metrics: &fakeIdentityMetrics{results: map[string]int{}},
		clock:   &gateClock{now: time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)},
	}
	h.gate = NewIngestGate(&fakeSeen{}, &fakeGateMetrics{blocked: map[string]int{}}, h.clock, log)
	h.warmup = NewWarmup(h.clock, 30*time.Second)
	h.sync = NewIdentitySync(h.source, h.cache, h.gate, h.warmup, h.metrics, log)
	h.reader = NewReadThroughResolver(h.cache, h.source, h.gate, h.clock, h.metrics, log)
	h.ingest = NewIngestLocationUseCase(
		&fakeTrackingPublisher{}, &fakeLatestLocationRepository{}, h.clock, fixedTrackingIDGenerator{value: "evt-1"},
		"default", &fakeLocationHistoryRepository{}, TelemetryConfig{}, h.reader, nil,
	).WithGate(h.gate).WithWarmup(h.warmup).WithIdentityMetrics(h.metrics)
	return h
}

func (h *identityHarness) send(imei string) (IngestLocationResult, error) {
	h.clock.now = h.clock.now.Add(time.Second)
	return h.ingest.Execute(context.Background(), IngestLocationCommand{IMEI: imei, Latitude: 6.5, Longitude: 3.4})
}

func TestIdentitySync_SnapshotPopulatesCacheAcrossPages(t *testing.T) {
	h := newIdentityHarness()
	h.source.pages = []IdentityPage{
		{Identities: []DeviceIdentity{{IMEI: "111", TenantID: "t1", DeviceID: "d1", VehicleUUID: "v1"}}, NextCursor: "d1"},
		{Identities: []DeviceIdentity{{IMEI: "222", TenantID: "t2", DeviceID: "d2"}}},
	}

	if err := h.sync.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	first, _ := h.cache.Resolve("111")
	second, _ := h.cache.Resolve("222")
	if first == nil || first.VehicleUUID != "v1" || second == nil || second.TenantID != "t2" {
		t.Fatalf("cache not populated: %+v %+v", first, second)
	}
	if h.metrics.size != 2 || !h.warmup.Ready() {
		t.Fatalf("size=%d ready=%v", h.metrics.size, h.warmup.Ready())
	}
}

func TestIdentitySync_FailedLoadKeepsIngestWarmingUntilTimeout(t *testing.T) {
	h := newIdentityHarness()
	h.source.pageErr = errors.New("fleet down")

	if err := h.sync.Reload(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if _, err := h.send("111"); !errors.Is(err, ErrWarmingUp) {
		t.Fatalf("want warming up, got %v", err)
	}

	h.clock.now = h.clock.now.Add(31 * time.Second)
	h.source.known["111"] = DeviceIdentity{IMEI: "111", TenantID: "t1"}
	result, err := h.send("111")
	if err != nil || !result.Published {
		t.Fatalf("after timeout want read-through publish, got %+v %v", result, err)
	}
}

func TestIdentitySync_ConflictKeepsFirstTenant(t *testing.T) {
	h := newIdentityHarness()
	_ = h.cache.Upsert(DeviceIdentity{IMEI: "111", TenantID: "first", DeviceID: "d-first"})
	h.source.pages = []IdentityPage{{Identities: []DeviceIdentity{{IMEI: "111", TenantID: "second", DeviceID: "d-second"}}}}

	if err := h.sync.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, _ := h.cache.Resolve("111")
	if got.TenantID != "first" || got.DeviceID != "d-first" || h.metrics.conflicts != 1 {
		t.Fatalf("conflict overwrote cache: %+v conflicts=%d", got, h.metrics.conflicts)
	}
}

func TestIdentitySync_HeldDeviceStaysSuspended(t *testing.T) {
	h := newIdentityHarness()
	h.gate.Replace(snapshotOf(nil, []string{"111"}), h.clock.now)
	_ = h.cache.Upsert(DeviceIdentity{IMEI: "222", TenantID: "t1", Suspended: true})
	h.source.pages = []IdentityPage{{Identities: []DeviceIdentity{
		{IMEI: "111", TenantID: "t1"},
		{IMEI: "222", TenantID: "t1", DeviceID: "d2"},
	}}}

	if err := h.sync.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	held, _ := h.cache.Resolve("111")
	kept, _ := h.cache.Resolve("222")
	if !held.Suspended || !kept.Suspended || kept.DeviceID != "d2" {
		t.Fatalf("flags lost: %+v %+v", held, kept)
	}
}

func TestReadThrough_HitRekeysAndCaches(t *testing.T) {
	h := newIdentityHarness()
	h.warmup.Done()
	h.source.known["111"] = DeviceIdentity{IMEI: "111", TenantID: "t1", DeviceID: "d1", VehicleUUID: "v1"}

	result, err := h.send("111")
	if err != nil || !result.Published {
		t.Fatalf("got %+v %v", result, err)
	}
	if cached, _ := h.cache.Resolve("111"); cached == nil || cached.VehicleUUID != "v1" {
		t.Fatalf("not cached: %+v", cached)
	}
	if _, err := h.send("111"); err != nil || h.source.lookups != 1 || h.metrics.results[resultHit] != 1 {
		t.Fatalf("lookups=%d results=%v err=%v", h.source.lookups, h.metrics.results, err)
	}
}

func TestReadThrough_DefinitiveUnknownQuarantinesAndIsNegativeCached(t *testing.T) {
	h := newIdentityHarness()
	h.warmup.Done()

	result, err := h.send("999")
	if err != nil || result.SkipReason != SkipUnknownDevice {
		t.Fatalf("got %+v %v", result, err)
	}
	if _, err := h.send("999"); err != nil || h.source.lookups != 1 {
		t.Fatalf("negative cache missed: lookups=%d err=%v", h.source.lookups, err)
	}
	if h.metrics.quarantined != 2 || h.metrics.results[resultMiss] != 1 || h.metrics.results[resultNegative] != 1 {
		t.Fatalf("metrics: %+v", h.metrics)
	}

	h.clock.now = h.clock.now.Add(61 * time.Second)
	h.source.known["999"] = DeviceIdentity{IMEI: "999", TenantID: "t1"}
	if r, err := h.send("999"); err != nil || !r.Published {
		t.Fatalf("expired negative entry should look up again: %+v %v", r, err)
	}
}

func TestReadThrough_FleetDownReturns503AndBacksOff(t *testing.T) {
	h := newIdentityHarness()
	h.warmup.Done()
	h.source.err = errors.New("connection refused")

	if _, err := h.send("111"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("want identity unavailable, got %v", err)
	}
	if _, err := h.send("222"); !errors.Is(err, ErrIdentityUnavailable) || h.source.lookups != 1 {
		t.Fatalf("breaker should short-circuit: lookups=%d err=%v", h.source.lookups, err)
	}
	if h.metrics.quarantined != 0 {
		t.Fatal("fleet failure must not quarantine")
	}

	h.clock.now = h.clock.now.Add(31 * time.Second)
	h.source.err = nil
	h.source.known["111"] = DeviceIdentity{IMEI: "111", TenantID: "t1"}
	if r, err := h.send("111"); err != nil || !r.Published {
		t.Fatalf("recovery failed: %+v %v", r, err)
	}
}

func TestReadThrough_EventUpsertClearsNegativeCache(t *testing.T) {
	h := newIdentityHarness()
	h.warmup.Done()
	if r, _ := h.send("111"); r.SkipReason != SkipUnknownDevice {
		t.Fatalf("got %+v", r)
	}

	_ = h.reader.EventSide().Upsert(DeviceIdentity{IMEI: "111", TenantID: "t1"})

	if r, err := h.send("111"); err != nil || !r.Published {
		t.Fatalf("got %+v %v", r, err)
	}
}
