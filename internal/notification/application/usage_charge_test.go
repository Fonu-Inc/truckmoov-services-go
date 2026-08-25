package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type recordingCharger struct {
	charges []UsageCharge
	err     error
}

func (c *recordingCharger) Charge(_ context.Context, charge UsageCharge) error {
	c.charges = append(c.charges, charge)
	return c.err
}

func newSendUseCaseWithCharger(charger UsageCharger, provider *fakeMessageProvider) *SendMessageUseCase {
	return NewSendMessageUseCase(
		newFakeOutboxRepository(),
		provider,
		&fakeDeliveryPublisher{},
		nil,
		charger,
		fixedNotificationClock{now: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "1"},
		"tenant-default",
	)
}

func TestBillableSendChargesTheWalletFirst(t *testing.T) {
	charger := &recordingCharger{}
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newSendUseCaseWithCharger(charger, provider)

	_, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID: "tenant-a", Channel: "sms", Recipient: "+2348000000000", Content: "hi",
	})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	if len(charger.charges) != 1 {
		t.Fatalf("expected one charge, got %d", len(charger.charges))
	}
	if charger.charges[0].Channel != "sms" || charger.charges[0].TenantID != "tenant-a" {
		t.Fatalf("charged the wrong thing: %+v", charger.charges[0])
	}
}

// The wallet is prepaid: a refused charge must stop the send, not follow it.
func TestRefusedChargeStopsTheSend(t *testing.T) {
	charger := &recordingCharger{err: ErrInsufficientFunds}
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newSendUseCaseWithCharger(charger, provider)

	message, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID: "tenant-a", Channel: "sms", Recipient: "+2348000000000", Content: "hi",
	})

	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("expected an insufficient funds error, got %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider was called %d times despite the refusal", provider.calls)
	}
	if message.Status != domain.StatusFailed {
		t.Fatalf("expected the message to be failed, got %s", message.Status)
	}
}

func TestDisabledChannelStopsTheSend(t *testing.T) {
	charger := &recordingCharger{err: ErrChannelDisabled}
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newSendUseCaseWithCharger(charger, provider)

	_, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID: "tenant-a", Channel: "whatsapp", Recipient: "+2348000000000", Content: "hi",
	})

	if !errors.Is(err, ErrChannelDisabled) {
		t.Fatalf("expected a disabled channel error, got %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider was called despite the channel being disabled")
	}
}

// Email rides on the tenant's own mail server and costs the platform nothing.
func TestEmailSendsWithoutACharge(t *testing.T) {
	charger := &recordingCharger{}
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newSendUseCaseWithCharger(charger, provider)

	if _, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID: "tenant-a", Channel: "email", Recipient: "a@b.com", Content: "hi",
	}); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	if len(charger.charges) != 0 {
		t.Fatalf("email was charged for: %+v", charger.charges)
	}
	if provider.calls != 1 {
		t.Fatalf("expected the email to send, provider calls = %d", provider.calls)
	}
}

// A deployment with charging unconfigured must not stop every send.
func TestSendsWhenNoChargerIsConfigured(t *testing.T) {
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newSendUseCaseWithCharger(nil, provider)

	if _, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID: "tenant-a", Channel: "sms", Recipient: "+2348000000000", Content: "hi",
	}); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	if provider.calls != 1 {
		t.Fatalf("expected the message to send, provider calls = %d", provider.calls)
	}
}

func TestChargeIsKeyedByMessageIDSoARetryChargesOnce(t *testing.T) {
	charger := &recordingCharger{}
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newSendUseCaseWithCharger(charger, provider)

	message, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID: "tenant-a", Channel: "sms", Recipient: "+2348000000000", Content: "hi",
	})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	if charger.charges[0].Reference != message.ID {
		t.Fatalf("charge reference %q is not the message id %q", charger.charges[0].Reference, message.ID)
	}
}

func TestIsBillable(t *testing.T) {
	for _, channel := range []string{"sms", "whatsapp", "voice", "ai"} {
		if !IsBillable(channel) {
			t.Fatalf("%s should be billable", channel)
		}
	}
	for _, channel := range []string{"email", "push", ""} {
		if IsBillable(channel) {
			t.Fatalf("%s should not be billable", channel)
		}
	}
}
