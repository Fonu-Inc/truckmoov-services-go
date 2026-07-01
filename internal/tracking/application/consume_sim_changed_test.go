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
	sim, _ := resolver.Resolve("d1")
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
	if sim, _ := resolver.Resolve("d1"); sim != nil {
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
