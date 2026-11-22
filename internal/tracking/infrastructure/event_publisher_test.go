package infrastructure

import (
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/domain"
)

func TestLocationEnvelopeCarriesDeviceAlarm(t *testing.T) {
	base := domain.LocationUpdate{
		EventID: "evt-1", OccurredAt: time.Now(), TenantID: "t1", VehicleID: "v1", DeviceID: "d1",
		FixTimeRFC: "2026-11-23T10:00:00Z",
	}
	withAlarm := base
	withAlarm.Alarm = " hardBraking "

	update, err := domain.NewLocationUpdate(withAlarm)
	if err != nil {
		t.Fatal(err)
	}
	if got := (&EventPublisher{}).locationEnvelope(update).Data["alarm"]; got != "hardBraking" {
		t.Fatalf("alarm = %v, want hardBraking", got)
	}

	plain, err := domain.NewLocationUpdate(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := (&EventPublisher{}).locationEnvelope(plain).Data["alarm"]; ok {
		t.Fatal("a fix without an alarm should not carry one")
	}
}
