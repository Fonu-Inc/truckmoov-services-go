package application

import (
	"context"
	"testing"

	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/logger"
)

func TestConsumeSIMChanged_UpsertAndEvict(t *testing.T) {
	resolver := &fakeSIMResolver{sims: map[string]DeviceSIM{}}
	uc := NewConsumeSIMChangedUseCase(resolver, logger.New("test"))

	assign := eventbus.EventEnvelope{
		TenantID: "t1",
		Data: map[string]any{
			"action":        "assigned",
			"device_id":     "d1",
			"iccid":         "8934071100001",
			"carrier":       "MTN",
			"plan":          "iot_basic",
			"status":        "assigned",
			"data_usage_mb": float64(120),
			"data_limit_mb": float64(500),
		},
	}
	if err := uc.Handle(context.Background(), assign); err != nil {
		t.Fatalf("handle assign: %v", err)
	}
	sim, _ := resolver.Resolve("t1", "d1")
	if sim == nil || sim.Carrier != "MTN" || sim.DataUsageMB != 120 {
		t.Fatalf("expected cached SIM after assign, got %+v", sim)
	}

	unassign := eventbus.EventEnvelope{
		TenantID: "t1",
		Data:     map[string]any{"action": "unassigned", "device_id": "d1"},
	}
	if err := uc.Handle(context.Background(), unassign); err != nil {
		t.Fatalf("handle unassign: %v", err)
	}
	if sim, _ := resolver.Resolve("t1", "d1"); sim != nil {
		t.Fatalf("expected SIM evicted after unassign, got %+v", sim)
	}
}

func TestConsumeSIMChanged_MissingDeviceIDNoOp(t *testing.T) {
	resolver := &fakeSIMResolver{sims: map[string]DeviceSIM{}}
	uc := NewConsumeSIMChangedUseCase(resolver, logger.New("test"))
	err := uc.Handle(context.Background(), eventbus.EventEnvelope{
		TenantID: "t1",
		Data:     map[string]any{"action": "assigned", "iccid": "x"},
	})
	if err != nil {
		t.Fatalf("expected no error on missing device_id, got %v", err)
	}
	if len(resolver.sims) != 0 {
		t.Fatalf("expected no cache write, got %d entries", len(resolver.sims))
	}
}

func TestConsumeSIMChanged_TenantIsolation(t *testing.T) {
	resolver := &fakeSIMResolver{sims: map[string]DeviceSIM{}}
	uc := NewConsumeSIMChangedUseCase(resolver, logger.New("test"))
	event := eventbus.EventEnvelope{TenantID: "t1", Data: map[string]any{"action": "assigned", "device_id": "d1", "carrier": "MTN"}}
	if err := uc.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if sim, _ := resolver.Resolve("t2", "d1"); sim != nil {
		t.Fatalf("other tenant must not see SIM, got %+v", sim)
	}
	if sim, _ := resolver.Resolve("t1", "d1"); sim == nil {
		t.Fatal("owning tenant must see SIM")
	}
}

func TestConsumeSIMChanged_MissingTenantNoOp(t *testing.T) {
	resolver := &fakeSIMResolver{sims: map[string]DeviceSIM{}}
	uc := NewConsumeSIMChangedUseCase(resolver, logger.New("test"))
	if err := uc.Handle(context.Background(), eventbus.EventEnvelope{Data: map[string]any{"action": "assigned", "device_id": "d1"}}); err != nil {
		t.Fatal(err)
	}
	if len(resolver.sims) != 0 {
		t.Fatalf("expected no cache write, got %d", len(resolver.sims))
	}
}

func TestConsumeSIMChanged_IgnoresForgedEvents(t *testing.T) {
	const subject = "tm.acme.sim.changed"
	event := eventbus.EventEnvelope{ID: "e1", Type: "sim.changed", TenantID: "acme", Data: map[string]any{"action": "assigned", "device_id": "d1"}}
	cases := map[string]eventbus.EventEnvelope{
		"unsigned":     deliveredOn(t, subject, event, false),
		"wrong tenant": deliveredOn(t, "tm.other.sim.changed", event, true),
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			resolver := &fakeSIMResolver{sims: map[string]DeviceSIM{}}
			handler := verifiedBy(NewConsumeSIMChangedUseCase(resolver, logger.New("test")).Handle)
			if err := handler(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if len(resolver.sims) != 0 {
				t.Fatalf("forged event changed SIMs: %+v", resolver.sims)
			}
		})
	}
}
