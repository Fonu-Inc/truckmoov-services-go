package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeEventClaims struct{ held map[string]bool }

func newFakeEventClaims() *fakeEventClaims { return &fakeEventClaims{held: map[string]bool{}} }

func (f *fakeEventClaims) Claim(_ context.Context, key string, _ time.Duration) (bool, error) {
	if f.held[key] {
		return false, nil
	}
	f.held[key] = true
	return true, nil
}

func (f *fakeEventClaims) Release(_ context.Context, key string) error {
	delete(f.held, key)
	return nil
}

func TestReplayedSignedAlertSendsOncePerRecipientAndChannel(t *testing.T) {
	setting := enabled()
	setting.Recipients = []string{"a@truckmoov.com", "b@truckmoov.com"}
	setting.Channels = []string{"email", "sms"}
	useCase, provider, _ := alertHarness(setting, nil)
	event := deliveredOn(t, "tm.system.alert.raised", alertEvent("critical"), true)
	handler := verifiedBy(useCase.Handle)

	for range 3 {
		if err := handler(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if provider.calls != 4 {
		t.Fatalf("expected 4 sends across 3 deliveries, got %d", provider.calls)
	}
}

func TestAlertRedeliveryAfterFailureRetriesOnlyTheFailed(t *testing.T) {
	setting := enabled()
	setting.Recipients = []string{"a@truckmoov.com", "b@truckmoov.com"}
	useCase, provider, _ := alertHarness(setting, nil)
	event := deliveredOn(t, "tm.system.alert.raised", alertEvent("critical"), true)

	provider.err = errors.New("smtp down")
	if err := useCase.Handle(context.Background(), event); err == nil {
		t.Fatal("expected failure to be returned for redelivery")
	}
	provider.err = nil
	provider.calls = 0
	if err := useCase.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 {
		t.Fatalf("expected both failed sends retried, got %d", provider.calls)
	}
	provider.calls = 0
	if err := useCase.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 {
		t.Fatalf("completed alert was sent again: %d", provider.calls)
	}
}

func TestAlertWithoutIDIsDropped(t *testing.T) {
	useCase, provider, _ := alertHarness(enabled(), nil)
	event := alertEvent("critical")
	event.ID = ""
	if err := useCase.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 {
		t.Fatal("alert without an id was sent")
	}
}

func TestReplayedSignedJobOfferSendsOnce(t *testing.T) {
	useCase, provider := jobOfferHarness()
	event := deliveredOn(t, "tm.acme.job.offer.created", jobOfferEvent(), true)
	handler := verifiedBy(useCase.Handle)

	for range 3 {
		if err := handler(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if provider.calls != 1 {
		t.Fatalf("expected 1 send across 3 deliveries, got %d", provider.calls)
	}
}
