package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/logger"
)

type gateClock struct{ now time.Time }

func (c *gateClock) Now() time.Time { return c.now }

type fakeSource struct {
	snapshot SuspensionSnapshot
	err      error
}

func (f *fakeSource) Load(context.Context) (SuspensionSnapshot, error) { return f.snapshot, f.err }

type fakeIndex struct{ members map[string]struct{} }

func (f *fakeIndex) Members() ([]string, error) {
	out := []string{}
	for m := range f.members {
		out = append(out, m)
	}
	return out, nil
}
func (f *fakeIndex) Add(imeis ...string) error {
	for _, i := range imeis {
		f.members[i] = struct{}{}
	}
	return nil
}
func (f *fakeIndex) Remove(imeis ...string) error {
	for _, i := range imeis {
		delete(f.members, i)
	}
	return nil
}

type fakeSeen struct{ writes []string }

func (f *fakeSeen) RecordSeen(id string, _ time.Time) error {
	f.writes = append(f.writes, id)
	return nil
}

type fakeGateMetrics struct {
	blocked map[string]int
	reloads int
}

func (f *fakeGateMetrics) Blocked(reason string) { f.blocked[reason]++ }
func (f *fakeGateMetrics) ReloadFailed()         { f.reloads++ }

func snapshotOf(tenants []string, imeis []string) SuspensionSnapshot {
	s := SuspensionSnapshot{Tenants: map[string]struct{}{}, IMEIs: map[string]struct{}{}}
	for _, t := range tenants {
		s.Tenants[t] = struct{}{}
	}
	for _, i := range imeis {
		s.IMEIs[i] = struct{}{}
	}
	return s
}

type gateHarness struct {
	gate      *IngestGate
	sync      *SuspensionSync
	source    *fakeSource
	resolver  *fakeIMEIResolver
	seen      *fakeSeen
	metrics   *fakeGateMetrics
	publisher *fakeTrackingPublisher
	clock     *gateClock
	ingest    *IngestLocationUseCase
}

func newGateHarness(policy string) *gateHarness {
	h := &gateHarness{
		source:    &fakeSource{},
		resolver:  newFakeIMEIResolver(),
		seen:      &fakeSeen{},
		metrics:   &fakeGateMetrics{blocked: map[string]int{}},
		publisher: &fakeTrackingPublisher{},
		clock:     &gateClock{now: time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)},
	}
	log := logger.New("test")
	h.gate = NewIngestGate(GateConfig{UnknownDevicePolicy: policy}, h.seen, h.metrics, h.clock, log)
	h.sync = NewSuspensionSync(h.source, h.gate, h.resolver, &fakeIndex{members: map[string]struct{}{}}, log)
	h.ingest = NewIngestLocationUseCase(
		h.publisher, &fakeLatestLocationRepository{}, h.clock, fixedTrackingIDGenerator{value: "evt-1"},
		"default", &fakeLocationHistoryRepository{}, TelemetryConfig{}, h.resolver, nil,
	).WithGate(h.gate)
	return h
}

func (h *gateHarness) send(cmd IngestLocationCommand) IngestLocationResult {
	cmd.Latitude, cmd.Longitude = 6.5, 3.4
	h.clock.now = h.clock.now.Add(time.Second)
	result, err := h.ingest.Execute(context.Background(), cmd)
	if err != nil {
		panic(err)
	}
	return result
}

func (h *gateHarness) reload(snapshot SuspensionSnapshot) {
	h.source.snapshot, h.source.err = snapshot, nil
	if err := h.sync.Reload(context.Background()); err != nil {
		panic(err)
	}
}

func envelopeFor(tenantID string) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{TenantID: tenantID, Data: map[string]any{"tenant_id": tenantID}}
}

func TestIngestGate_SuspendAndReinstateByEvent(t *testing.T) {
	h := newGateHarness("default")
	events := NewConsumeTenantSuspensionUseCase(h.gate)
	cmd := IngestLocationCommand{TenantID: "t1", VehicleID: "v1", DeviceID: "d1"}

	if r := h.send(cmd); !r.Published {
		t.Fatalf("open tenant must pass, got %+v", r)
	}
	h.publisher.location.EventID = ""

	_ = events.HandleSuspended(context.Background(), envelopeFor("t1"))
	r := h.send(cmd)
	if !r.Skipped || r.SkipReason != SkipTenantSuspended || r.Published {
		t.Fatalf("suspended tenant must be dropped, got %+v", r)
	}
	if h.publisher.location.EventID != "" {
		t.Fatal("blocked message must not publish")
	}
	if h.metrics.blocked[SkipTenantSuspended] != 1 {
		t.Fatalf("blocked counter, got %v", h.metrics.blocked)
	}

	_ = events.HandleReinstated(context.Background(), envelopeFor("t1"))
	if r := h.send(cmd); !r.Published {
		t.Fatalf("reinstated tenant must reopen, got %+v", r)
	}
}

func TestIngestGate_DeviceBlocking(t *testing.T) {
	const imei = "359779090012345"
	tests := []struct {
		name     string
		identity *DeviceIdentity
		imeis    []string
		cmd      IngestLocationCommand
		want     string
	}{
		{"warm identity suspended", &DeviceIdentity{IMEI: imei, TenantID: "t1", Suspended: true}, nil, IngestLocationCommand{IMEI: imei}, SkipDeviceSuspended},
		{"cold cache imei in list", nil, []string{imei}, IngestLocationCommand{IMEI: imei, TenantID: "t9"}, SkipDeviceSuspended},
		{"spoofed body tenant loses to resolved tenant", &DeviceIdentity{IMEI: imei, TenantID: "bad"}, nil, IngestLocationCommand{IMEI: imei, TenantID: "good"}, SkipTenantSuspended},
		{"warm identity not suspended beats stale list", &DeviceIdentity{IMEI: imei, TenantID: "t1"}, []string{imei}, IngestLocationCommand{IMEI: imei}, ""},
		{"unknown imei default policy passes", nil, nil, IngestLocationCommand{IMEI: imei, TenantID: "t9"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newGateHarness("default")
			if tt.identity != nil {
				_ = h.resolver.Upsert(*tt.identity)
			}
			h.gate.Replace(snapshotOf([]string{"bad"}, tt.imeis), h.clock.now)
			r := h.send(tt.cmd)
			if r.SkipReason != tt.want {
				t.Fatalf("want %q got %+v", tt.want, r)
			}
		})
	}
}

func TestIngestGate_SpoofedTenantCannotUnblockSuspendedTenantDevice(t *testing.T) {
	h := newGateHarness("default")
	_ = h.resolver.Upsert(DeviceIdentity{IMEI: "111", TenantID: "t-suspended"})
	h.gate.Replace(snapshotOf([]string{"t-suspended"}, nil), h.clock.now)
	r := h.send(IngestLocationCommand{IMEI: "111", TenantID: "t-clean"})
	if r.SkipReason != SkipTenantSuspended {
		t.Fatalf("got %+v", r)
	}
}

func TestIngestGate_UnknownIMEI(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		tenant string
		list   []string
		want   string
	}{
		{"default policy passes clean tenant", "default", "t1", nil, ""},
		{"default policy blocks suspended body tenant", "default", "t-bad", nil, SkipTenantSuspended},
		{"default policy blocks listed imei", "default", "t1", []string{"999"}, SkipDeviceSuspended},
		{"reject policy drops", "reject", "t1", nil, SkipUnknownDevice},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newGateHarness(tt.policy)
			h.gate.Replace(snapshotOf([]string{"t-bad"}, tt.list), h.clock.now)
			r := h.send(IngestLocationCommand{IMEI: "999", TenantID: tt.tenant})
			if r.SkipReason != tt.want {
				t.Fatalf("want %q got %+v", tt.want, r)
			}
		})
	}
}

func TestSuspensionSync_ReloadHealsMissedEventsBothWays(t *testing.T) {
	h := newGateHarness("default")
	_ = h.resolver.Upsert(DeviceIdentity{IMEI: "111", TenantID: "t1"})
	cmd := IngestLocationCommand{TenantID: "t1", VehicleID: "v", DeviceID: "d"}

	h.reload(snapshotOf([]string{"t1"}, []string{"111"}))
	if r := h.send(cmd); r.SkipReason != SkipTenantSuspended {
		t.Fatalf("missed suspend event must be recovered, got %+v", r)
	}
	if id, _ := h.resolver.Resolve("111"); !id.Suspended {
		t.Fatal("stale identity flag must be set by reload")
	}
	if r := h.send(IngestLocationCommand{IMEI: "111"}); r.SkipReason != SkipTenantSuspended {
		t.Fatalf("got %+v", r)
	}

	h.reload(snapshotOf(nil, nil))
	if r := h.send(cmd); !r.Published {
		t.Fatalf("missed reinstate event must be recovered, got %+v", r)
	}
	if id, _ := h.resolver.Resolve("111"); id.Suspended {
		t.Fatal("stale identity flag must be cleared by reload")
	}
}

func TestSuspensionSync_EventDuringLoadIsNotUndone(t *testing.T) {
	h := newGateHarness("default")
	events := NewConsumeTenantSuspensionUseCase(h.gate)
	started := h.gate.Begin()
	h.clock.now = h.clock.now.Add(time.Second)
	_ = events.HandleSuspended(context.Background(), envelopeFor("t-new"))
	h.gate.Replace(snapshotOf(nil, nil), started)
	if r := h.send(IngestLocationCommand{TenantID: "t-new", VehicleID: "v", DeviceID: "d"}); r.SkipReason != SkipTenantSuspended {
		t.Fatalf("got %+v", r)
	}
}

func TestSuspensionSync_StartupAndRestart(t *testing.T) {
	h := newGateHarness("default")
	h.source.err = errors.New("auth down")
	if err := h.sync.LoadInitial(context.Background(), 2, time.Millisecond); err == nil {
		t.Fatal("expected failure")
	}
	if h.gate.Ready() {
		t.Fatal("gate must stay not ready until first load succeeds")
	}
	if h.metrics.reloads != 2 {
		t.Fatalf("failures counted, got %d", h.metrics.reloads)
	}

	h.source.err = nil
	h.source.snapshot = snapshotOf([]string{"t1"}, nil)
	if err := h.sync.LoadInitial(context.Background(), 2, time.Millisecond); err != nil || !h.gate.Ready() {
		t.Fatalf("load should succeed: %v ready=%v", err, h.gate.Ready())
	}

	restarted := NewIngestGate(GateConfig{}, h.seen, h.metrics, h.clock, logger.New("test"))
	if restarted.Ready() {
		t.Fatal("fresh pod starts not ready")
	}
	NewSuspensionSync(h.source, restarted, h.resolver, &fakeIndex{members: map[string]struct{}{}}, logger.New("test")).Reload(context.Background())
	if restarted.Evaluate("t1", nil, "") != SkipTenantSuspended {
		t.Fatal("restarted pod must block suspended tenant after load")
	}
}

func TestSuspensionSync_ReloadFailureKeepsLastSets(t *testing.T) {
	h := newGateHarness("default")
	h.reload(snapshotOf([]string{"t1"}, []string{"111"}))
	h.source.err = errors.New("fleet 503")
	if err := h.sync.Reload(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if h.gate.Evaluate("t1", nil, "") != SkipTenantSuspended || h.gate.Evaluate("x", nil, "111") != SkipDeviceSuspended {
		t.Fatal("last known sets must stay in force")
	}
	if h.metrics.reloads != 1 {
		t.Fatalf("reload failure counted, got %d", h.metrics.reloads)
	}
}

func TestIngestGate_SeenRecordAndLogAreThrottled(t *testing.T) {
	h := newGateHarness("default")
	h.gate.Replace(snapshotOf(nil, []string{"111"}), h.clock.now)
	for i := 0; i < 5; i++ {
		h.send(IngestLocationCommand{IMEI: "111"})
	}
	if len(h.seen.writes) != 1 || h.metrics.blocked[SkipDeviceSuspended] != 5 {
		t.Fatalf("writes=%v blocked=%v", h.seen.writes, h.metrics.blocked)
	}
	h.clock.now = h.clock.now.Add(61 * time.Second)
	h.send(IngestLocationCommand{IMEI: "111"})
	if len(h.seen.writes) != 2 {
		t.Fatalf("expected a second write after a minute, got %v", h.seen.writes)
	}
}

func TestConsumeDeviceIdentity_KeepsSuspendedFlagAndNeverEvicts(t *testing.T) {
	resolver := newFakeIMEIResolver()
	use := NewConsumeDeviceIdentityUseCase(resolver, logger.New("test"))
	handle := func(suspended bool) {
		_ = use.Handle(context.Background(), eventbus.EventEnvelope{TenantID: "t1", Data: map[string]any{
			"imei": "111", "action": "updated", "device_id": "d", "vehicle_id": "v", "suspended": suspended,
		}})
	}
	handle(true)
	if id, _ := resolver.Resolve("111"); id == nil || !id.Suspended || id.VehicleUUID != "v" {
		t.Fatalf("got %+v", id)
	}
	handle(false)
	if id, _ := resolver.Resolve("111"); id == nil || id.Suspended {
		t.Fatalf("reinstate must flip the flag, got %+v", id)
	}
}

func TestIngestGate_NilGateAndDisabledPassThrough(t *testing.T) {
	var gate *IngestGate
	if gate.Evaluate("t", nil, "1") != "" || !gate.Ready() {
		t.Fatal("nil gate must allow everything")
	}
}
