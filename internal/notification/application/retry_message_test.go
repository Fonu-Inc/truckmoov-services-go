package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

func failedSMS() domain.OutboxMessage {
	return domain.OutboxMessage{ID: "msg-1", TenantID: "tenant-a", Channel: "sms", Recipient: "+2348000000000", Content: "hi", Status: domain.StatusFailed}
}

func newRetryUseCaseFor(message domain.OutboxMessage, charger UsageCharger, provider *fakeMessageProvider) *RetryMessageUseCase {
	repository := newFakeOutboxRepository()
	repository.data[message.ID] = message
	return NewRetryMessageUseCase(
		repository,
		provider,
		&fakeDeliveryPublisher{},
		fixedNotificationClock{now: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "1"},
	).WithCharger(charger)
}

func TestRetryChargesTheWalletBeforeResending(t *testing.T) {
	charger := &recordingCharger{}
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newRetryUseCaseFor(failedSMS(), charger, provider)

	if _, err := useCase.Execute(context.Background(), "tenant-a", "msg-1"); err != nil {
		t.Fatalf("retry failed: %v", err)
	}

	if len(charger.charges) != 1 || charger.charges[0].Reference != "msg-1" {
		t.Fatalf("expected one charge keyed by the message id, got %+v", charger.charges)
	}
	if provider.calls != 1 {
		t.Fatalf("expected one send, got %d", provider.calls)
	}
}

func TestRetryRefusedChargeDoesNotResend(t *testing.T) {
	charger := &recordingCharger{err: ErrInsufficientFunds}
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newRetryUseCaseFor(failedSMS(), charger, provider)

	_, err := useCase.Execute(context.Background(), "tenant-a", "msg-1")

	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("expected the refusal, got %v", err)
	}
	if provider.calls != 0 {
		t.Fatal("a refused charge must not send")
	}
}

func TestRetryOnlyTakesAFailedMessage(t *testing.T) {
	delivered := failedSMS()
	delivered.Status = domain.StatusSent
	provider := &fakeMessageProvider{status: domain.StatusSent}
	useCase := newRetryUseCaseFor(delivered, &recordingCharger{}, provider)

	_, err := useCase.Execute(context.Background(), "tenant-a", "msg-1")

	if !errors.Is(err, ErrMessageNotRetryable) {
		t.Fatalf("expected ErrMessageNotRetryable, got %v", err)
	}
	if provider.calls != 0 {
		t.Fatal("a sent message must not be sent again")
	}
}
