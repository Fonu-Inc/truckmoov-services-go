package application

import (
	"context"
	"testing"

	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/logger"
)

const busTestSecret = "bus-test-secret"

func deliveredOn(t *testing.T, subject string, e eventbus.EventEnvelope, signed bool) eventbus.EventEnvelope {
	t.Helper()
	e.Subject = subject
	if signed {
		sig, err := eventbus.BusSignature(busTestSecret, subject, e)
		if err != nil {
			t.Fatal(err)
		}
		e.BusSignature = sig
	}
	return e
}

func verifiedBy(handler eventbus.Handler) eventbus.Handler {
	return eventbus.Verified(handler, busTestSecret, eventbus.VerifyOptions{}, logger.New("test"))
}

func identityEvent(tenant string) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{
		ID: "evt-1", Type: "device.identity.changed", TenantID: tenant,
		Data: map[string]any{"imei": "359779090012345", "device_id": "d1", "vehicle_id": "v1", "action": "registered"},
	}
}

func TestDeviceIdentityIgnoresForgedEvents(t *testing.T) {
	const subject = "tm.acme.device.identity.changed"
	cases := map[string]eventbus.EventEnvelope{
		"unsigned":     deliveredOn(t, subject, identityEvent("acme"), false),
		"wrong tenant": deliveredOn(t, "tm.other.device.identity.changed", identityEvent("acme"), true),
		"tampered": func() eventbus.EventEnvelope {
			e := deliveredOn(t, subject, identityEvent("acme"), true)
			e.Data["vehicle_id"] = "stolen"
			return e
		}(),
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			resolver := newFakeIMEIResolver()
			handler := verifiedBy(NewConsumeDeviceIdentityUseCase(resolver, logger.New("test")).Handle)
			if err := handler(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			if len(resolver.entries) != 0 {
				t.Fatalf("forged event changed identities: %+v", resolver.entries)
			}
		})
	}
}

func TestDeviceIdentityAppliesSignedEvent(t *testing.T) {
	resolver := newFakeIMEIResolver()
	handler := verifiedBy(NewConsumeDeviceIdentityUseCase(resolver, logger.New("test")).Handle)
	event := deliveredOn(t, "tm.acme.device.identity.changed", identityEvent("acme"), true)
	if err := handler(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if got, _ := resolver.Resolve("359779090012345"); got == nil || got.TenantID != "acme" {
		t.Fatalf("signed event not applied: %+v", got)
	}
}

func suspensionEvent(tenant, dataTenant string) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{ID: "evt-1", Type: "tenant.suspended", TenantID: tenant, Data: map[string]any{"tenant_id": dataTenant}}
}

func blocked(h *gateHarness, tenant string) bool {
	return h.send(IngestLocationCommand{TenantID: tenant, VehicleID: "v1", DeviceID: "d1"}).Skipped
}

func TestTenantSuspensionIgnoresForgedEvents(t *testing.T) {
	h := newGateHarness("default")
	uc := NewConsumeTenantSuspensionUseCase(h.gate)
	suspend := verifiedBy(uc.HandleSuspended)

	unsigned := deliveredOn(t, "tm.t1.tenant.suspended", suspensionEvent("t1", "t1"), false)
	wrongTenant := deliveredOn(t, "tm.t2.tenant.suspended", suspensionEvent("t1", "t1"), true)
	for _, event := range []eventbus.EventEnvelope{unsigned, wrongTenant} {
		if err := suspend(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if blocked(h, "t1") || blocked(h, "t2") {
		t.Fatal("forged suspension took effect")
	}
}

func TestTenantSuspensionSignedEventUsesEnvelopeTenantNotPayload(t *testing.T) {
	h := newGateHarness("default")
	uc := NewConsumeTenantSuspensionUseCase(h.gate)

	event := deliveredOn(t, "tm.t1.tenant.suspended", suspensionEvent("t1", "t2"), true)
	if err := verifiedBy(uc.HandleSuspended)(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if !blocked(h, "t1") {
		t.Fatal("signed suspension did not apply to the subject tenant")
	}
	if blocked(h, "t2") {
		t.Fatal("payload tenant_id must not pick the tenant")
	}

	reinstate := deliveredOn(t, "tm.t1.tenant.reinstated", suspensionEvent("t1", "t2"), true)
	if err := verifiedBy(uc.HandleReinstated)(context.Background(), reinstate); err != nil {
		t.Fatal(err)
	}
	if blocked(h, "t1") {
		t.Fatal("signed reinstatement did not apply")
	}
}
