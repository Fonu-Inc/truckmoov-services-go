package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingAlerter struct{ imeis []string }

func (r *recordingAlerter) NoisyIMEI(_ context.Context, imei string, _ int) {
	r.imeis = append(r.imeis, imei)
}

func TestIngestLocation_BlockedUnknownIMEIIsRefusedAndNotRecorded(t *testing.T) {
	store := &fakeQuarantineStore{counts: map[string]int{"359779090099999": 2}}
	clock := fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)}
	quarantine := NewQuarantine(store, clock)
	if err := quarantine.Block(context.Background(), "359779090099999"); err != nil {
		t.Fatalf("block: %v", err)
	}
	useCase := NewIngestLocationUseCase(
		&fakeTrackingPublisher{}, &fakeLatestLocationRepository{}, clock,
		fixedTrackingIDGenerator{value: "evt-1"}, "default", &fakeLocationHistoryRepository{},
		TelemetryConfig{}, newFakeIMEIResolver(), nil,
	).WithQuarantine(quarantine)

	result, err := useCase.Execute(context.Background(), IngestLocationCommand{IMEI: "359779090099999", Latitude: 6.5, Longitude: 3.4})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !result.Skipped || result.SkipReason != SkipBlocked {
		t.Fatalf("expected blocked skip, got %+v", result)
	}
	if store.counts["359779090099999"] != 2 {
		t.Fatalf("blocked sender must not be recorded again, got %v", store.counts)
	}
}

func TestQuarantine_BlockOfUnknownRowIsNotFound(t *testing.T) {
	clock := fixedTrackingClock{now: time.Now()}
	quarantine := NewQuarantine(&fakeQuarantineStore{}, clock)
	if err := quarantine.Block(context.Background(), "nobody"); !errors.Is(err, ErrQuarantineNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if quarantine.IsBlocked("nobody") {
		t.Fatalf("failed block must not take effect")
	}
}

func TestQuarantine_ReloadPicksUpBlocksFromOtherInstances(t *testing.T) {
	store := &fakeQuarantineStore{blocked: []string{"imei-9"}}
	quarantine := NewQuarantine(store, fixedTrackingClock{now: time.Now()})
	if err := quarantine.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !quarantine.IsBlocked("imei-9") || quarantine.IsBlocked("imei-1") {
		t.Fatalf("blocked set must mirror the store")
	}
}

func TestQuarantine_DismissLiftsNothingButRecordsStatus(t *testing.T) {
	store := &fakeQuarantineStore{counts: map[string]int{"imei-1": 1}}
	quarantine := NewQuarantine(store, fixedTrackingClock{now: time.Now()})
	if err := quarantine.Dismiss(context.Background(), "imei-1"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if quarantine.IsBlocked("imei-1") {
		t.Fatalf("dismiss must not block")
	}
}

func TestQuarantine_AlertsOncePerHourPastThreshold(t *testing.T) {
	now := time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)
	alerter := &recordingAlerter{}
	quarantine := NewQuarantine(&fakeQuarantineStore{}, tickClock{now: &now}).WithAlerter(alerter)
	ctx := context.Background()

	for i := 0; i < quarantineAlertThreshold; i++ {
		quarantine.Observe(ctx, "imei-1")
	}
	if len(alerter.imeis) != 0 {
		t.Fatalf("no alert at the threshold, got %v", alerter.imeis)
	}
	quarantine.Observe(ctx, "imei-1")
	quarantine.Observe(ctx, "imei-1")
	if len(alerter.imeis) != 1 {
		t.Fatalf("expected one alert, got %v", alerter.imeis)
	}

	now = now.Add(quarantineAlertWindow)
	for i := 0; i <= quarantineAlertThreshold; i++ {
		quarantine.Observe(ctx, "imei-1")
	}
	if len(alerter.imeis) != 2 {
		t.Fatalf("expected a second alert in the next hour, got %v", alerter.imeis)
	}
}
