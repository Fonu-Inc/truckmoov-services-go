package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

func busVerifyLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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
	return eventbus.Verified(handler, busTestSecret, eventbus.VerifyOptions{}, busVerifyLogger())
}

func TestForgedPlatformAlertSendsNothing(t *testing.T) {
	const subject = "tm.system.alert.raised"
	tampered := deliveredOn(t, subject, alertEvent("critical"), true)
	tampered.Data["subject"] = "Send everyone a phishing link"
	otherTenant := alertEvent("critical")
	otherTenant.TenantID = "acme"
	cases := map[string]eventbus.EventEnvelope{
		"unsigned":        deliveredOn(t, subject, alertEvent("critical"), false),
		"tampered":        tampered,
		"customer tenant": deliveredOn(t, subject, otherTenant, true),
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			useCase, provider, _ := alertHarness(enabled(), nil)
			if err := verifiedBy(useCase.Handle)(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			if provider.calls != 0 {
				t.Fatalf("forged alert sent %d messages", provider.calls)
			}
		})
	}
}

func TestSignedPlatformAlertIsSent(t *testing.T) {
	useCase, provider, _ := alertHarness(enabled(), nil)
	event := deliveredOn(t, "tm.system.alert.raised", alertEvent("critical"), true)
	if err := verifiedBy(useCase.Handle)(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatalf("expected 1 send, got %d", provider.calls)
	}
}

func jobOfferHarness() (*ConsumeJobOfferNotificationUseCase, *fakeMessageProvider) {
	provider := &fakeMessageProvider{status: domain.StatusSent}
	send := NewSendMessageUseCase(
		newFakeOutboxRepository(), provider, &fakeDeliveryPublisher{}, nil, &recordingCharger{},
		fixedNotificationClock{now: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "1"}, "system",
	)
	return NewConsumeJobOfferNotificationUseCase(send, newFakeEventClaims(), busVerifyLogger()), provider
}

func jobOfferEvent() eventbus.EventEnvelope {
	return eventbus.EventEnvelope{
		ID: "evt-1", Type: "job.offer.created", TenantID: "acme",
		Data: map[string]any{"offer_id": "o1", "recipient": "+2348000000000", "channel": "whatsapp"},
	}
}

func TestForgedJobOfferSendsNothing(t *testing.T) {
	const subject = "tm.acme.job.offer.created"
	cases := map[string]eventbus.EventEnvelope{
		"unsigned":     deliveredOn(t, subject, jobOfferEvent(), false),
		"wrong tenant": deliveredOn(t, "tm.victim.job.offer.created", jobOfferEvent(), true),
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			useCase, provider := jobOfferHarness()
			if err := verifiedBy(useCase.Handle)(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			if provider.calls != 0 {
				t.Fatalf("forged offer sent %d messages", provider.calls)
			}
		})
	}
}

func TestSignedJobOfferIsSent(t *testing.T) {
	useCase, provider := jobOfferHarness()
	event := deliveredOn(t, "tm.acme.job.offer.created", jobOfferEvent(), true)
	if err := verifiedBy(useCase.Handle)(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatalf("expected 1 send, got %d", provider.calls)
	}
}
