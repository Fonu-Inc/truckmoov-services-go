package application

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

var errTestProviderFailed = errors.New("provider failure")

func buildConsumeUseCase(provider *fakeMessageProvider) *ConsumeNotificationUseCase {
	repo := newFakeOutboxRepository()
	publisher := &fakeDeliveryPublisher{}
	clock := fixedNotificationClock{now: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)}
	ids := fixedNotificationIDGenerator{id: "1"}
	send := NewSendMessageUseCase(repo, provider, publisher, nil, nil, clock, ids, "default")
	return NewConsumeNotificationUseCase(send, testSigningSecret, slog.Default())
}

func makeNotificationEnvelope(recipient, content, channel string) eventbus.EventEnvelope {
	data := map[string]any{
		"recipient": recipient,
		"content":   content,
	}
	if channel != "" {
		data["channel"] = channel
	}
	return signedEnvelope(eventbus.EventEnvelope{
		TenantID: "tenant-1",
		Data:     data,
	})
}

const testSigningSecret = "test-signing-secret"

func signedEnvelope(envelope eventbus.EventEnvelope) eventbus.EventEnvelope {
	envelope.Subject = "tm." + envelope.TenantID + ".notification.send"
	envelope.Signature = SignatureFor(testSigningSecret, envelope)
	return envelope
}

func TestConsumeNotificationUseCase_Handle_SendsMessage(t *testing.T) {
	provider := &fakeMessageProvider{status: "sent"}
	uc := buildConsumeUseCase(provider)

	err := uc.Handle(context.Background(), makeNotificationEnvelope("+23400000001", "Hello driver", "whatsapp"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConsumeNotificationUseCase_Handle_DefaultsChannelToWhatsapp(t *testing.T) {
	provider := &fakeMessageProvider{status: "sent"}
	uc := buildConsumeUseCase(provider)

	err := uc.Handle(context.Background(), makeNotificationEnvelope("+23400000002", "content", ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConsumeNotificationUseCase_Handle_MissingRecipientIsNoop(t *testing.T) {
	provider := &fakeMessageProvider{status: "sent"}
	uc := buildConsumeUseCase(provider)

	err := uc.Handle(context.Background(), makeNotificationEnvelope("", "content", "whatsapp"))
	if err != nil {
		t.Fatalf("expected no error for missing recipient, got: %v", err)
	}
}

func TestConsumeNotificationUseCase_Handle_MissingContentIsNoop(t *testing.T) {
	provider := &fakeMessageProvider{status: "sent"}
	uc := buildConsumeUseCase(provider)

	err := uc.Handle(context.Background(), makeNotificationEnvelope("+23400000003", "", "whatsapp"))
	if err != nil {
		t.Fatalf("expected no error for missing content, got: %v", err)
	}
}

func TestConsumeNotificationUseCase_Handle_ProviderErrorPropagates(t *testing.T) {
	provider := &fakeMessageProvider{err: errTestProviderFailed}
	uc := buildConsumeUseCase(provider)

	err := uc.Handle(context.Background(), makeNotificationEnvelope("+23400000004", "test", "whatsapp"))
	if err == nil {
		t.Fatalf("expected error from provider failure")
	}
}

func TestConsumeNotificationUseCase_Handle_PassesNotificationTypeToTheLog(t *testing.T) {
	log := &recordingDeliveryLog{}
	uc := NewConsumeNotificationUseCase(newSendUseCaseWithLog(log, &fakeMessageProvider{status: "sent"}), testSigningSecret, slog.Default())
	envelope := makeNotificationEnvelope("installer@fieldops.ng", "Job cancelled\n\nJob #FL-1025 has been cancelled.", "email")
	envelope.TenantID = "system"
	envelope.Data["notification_type"] = "job_cancelled"
	envelope = signedEnvelope(envelope)

	if err := uc.Handle(context.Background(), envelope); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(log.entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(log.entries))
	}
	if log.entries[0].NotificationType != "job_cancelled" || log.entries[0].TenantID != "system" {
		t.Fatalf("logged the wrong type or tenant: %+v", log.entries[0])
	}
}

func TestConsumeNotificationUseCase_Handle_UntypedSendIsGeneric(t *testing.T) {
	log := &recordingDeliveryLog{}
	uc := NewConsumeNotificationUseCase(newSendUseCaseWithLog(log, &fakeMessageProvider{status: "sent"}), testSigningSecret, slog.Default())

	if err := uc.Handle(context.Background(), makeNotificationEnvelope("+23400000009", "hello", "sms")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(log.entries) != 1 || log.entries[0].NotificationType != "generic" {
		t.Fatalf("expected a generic entry, got %+v", log.entries)
	}
}

type idempotentCharger struct {
	settled map[string]bool
	debits  int
	err     error
}

func (c *idempotentCharger) Charge(_ context.Context, charge UsageCharge) error {
	if c.err != nil {
		return c.err
	}
	if !c.settled[charge.Reference] {
		c.settled[charge.Reference] = true
		c.debits++
	}
	return nil
}

func buildChargedConsumeUseCase(provider *fakeMessageProvider, charger UsageCharger) (*ConsumeNotificationUseCase, *fakeOutboxRepository) {
	repo := newFakeOutboxRepository()
	clock := fixedNotificationClock{now: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)}
	send := NewSendMessageUseCase(repo, provider, &fakeDeliveryPublisher{}, nil, charger, clock, &campaignIDs{}, "default")
	return NewConsumeNotificationUseCase(send, testSigningSecret, slog.Default()), repo
}

func TestConsumeRedeliveryChargesWithTheSameReference(t *testing.T) {
	charger := &recordingCharger{}
	uc, _ := buildChargedConsumeUseCase(&fakeMessageProvider{status: "sent"}, charger)
	envelope := makeNotificationEnvelope("+23400000001", "hi", "sms")
	envelope.ID = "evt-42"
	envelope = signedEnvelope(envelope)

	for range 2 {
		if err := uc.Handle(context.Background(), envelope); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}

	if len(charger.charges) != 2 || charger.charges[0].Reference != charger.charges[1].Reference {
		t.Fatalf("redelivery changed the reference: %+v", charger.charges)
	}
	if charger.charges[0].Reference != "msg-evt-42" {
		t.Fatalf("reference not derived from the envelope id: %q", charger.charges[0].Reference)
	}
}

func TestConsumeRedeliveryDebitsOnceWithIdempotentCharger(t *testing.T) {
	charger := &idempotentCharger{settled: map[string]bool{}}
	uc, _ := buildChargedConsumeUseCase(&fakeMessageProvider{status: "sent"}, charger)
	envelope := makeNotificationEnvelope("+23400000001", "hi", "sms")
	envelope.ID = "evt-7"
	envelope = signedEnvelope(envelope)

	_ = uc.Handle(context.Background(), envelope)
	_ = uc.Handle(context.Background(), envelope)

	if charger.debits != 1 {
		t.Fatalf("expected one debit across redelivery, got %d", charger.debits)
	}
}

func TestConsumeInsufficientFundsIsTerminalAndRecordedFailed(t *testing.T) {
	provider := &fakeMessageProvider{status: "sent"}
	uc, repo := buildChargedConsumeUseCase(provider, &recordingCharger{err: ErrInsufficientFunds})
	envelope := makeNotificationEnvelope("+23400000001", "hi", "sms")
	envelope.ID = "evt-9"
	envelope = signedEnvelope(envelope)

	if err := uc.Handle(context.Background(), envelope); err != nil {
		t.Fatalf("a refused charge must be acked, got %v", err)
	}

	message, ok := repo.FindByID("msg-evt-9")
	if !ok || message.Status != "failed" {
		t.Fatalf("expected the message recorded as failed, got %+v ok=%v", message, ok)
	}
	if provider.calls != 0 {
		t.Fatalf("provider called %d times despite the refusal", provider.calls)
	}
}

func TestConsumeTransientChargeErrorStillRedelivers(t *testing.T) {
	uc, _ := buildChargedConsumeUseCase(&fakeMessageProvider{status: "sent"}, &recordingCharger{err: errors.New("auth down")})
	envelope := makeNotificationEnvelope("+23400000001", "hi", "sms")
	envelope.ID = "evt-1"
	envelope = signedEnvelope(envelope)

	if err := uc.Handle(context.Background(), envelope); err == nil {
		t.Fatal("expected the error to propagate for redelivery")
	}
}

func TestConsumeRejectsUnverifiedEvents(t *testing.T) {
	cases := map[string]func(e eventbus.EventEnvelope) eventbus.EventEnvelope{
		"unsigned": func(e eventbus.EventEnvelope) eventbus.EventEnvelope { e.Signature = ""; return e },
		"wrong secret": func(e eventbus.EventEnvelope) eventbus.EventEnvelope {
			e.Signature = SignatureFor("other-secret", e)
			return e
		},
		"tenant swapped after signing": func(e eventbus.EventEnvelope) eventbus.EventEnvelope {
			e.TenantID = "victim"
			e.Subject = "tm.victim.notification.send"
			return e
		},
		"subject tenant differs from payload": func(e eventbus.EventEnvelope) eventbus.EventEnvelope {
			e.Subject = "tm.victim.notification.send"
			return e
		},
		"content tampered": func(e eventbus.EventEnvelope) eventbus.EventEnvelope {
			e.Data["content"] = "pay to account 123"
			return e
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			charger := &recordingCharger{}
			uc, _ := buildChargedConsumeUseCase(&fakeMessageProvider{status: "sent"}, charger)
			envelope := tamper(makeNotificationEnvelope("+23400000001", "hi", "sms"))

			if err := uc.Handle(context.Background(), envelope); err != nil {
				t.Fatalf("a rejected event is acked, not retried: %v", err)
			}
			if len(charger.charges) != 0 {
				t.Fatalf("rejected event reached billing: %+v", charger.charges)
			}
		})
	}
}

func TestConsumeRejectsEverythingWithoutASigningSecret(t *testing.T) {
	charger := &recordingCharger{}
	_, _ = buildChargedConsumeUseCase(&fakeMessageProvider{status: "sent"}, charger)
	repo := newFakeOutboxRepository()
	clock := fixedNotificationClock{now: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)}
	send := NewSendMessageUseCase(repo, &fakeMessageProvider{status: "sent"}, &fakeDeliveryPublisher{}, nil, charger, clock, &campaignIDs{}, "default")
	uc := NewConsumeNotificationUseCase(send, "", slog.Default())

	_ = uc.Handle(context.Background(), makeNotificationEnvelope("+23400000001", "hi", "sms"))
	if len(charger.charges) != 0 {
		t.Fatalf("event processed without a secret: %+v", charger.charges)
	}
}
