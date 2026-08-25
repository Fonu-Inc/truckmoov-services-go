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
	return NewConsumeNotificationUseCase(send, slog.Default())
}

func makeNotificationEnvelope(recipient, content, channel string) eventbus.EventEnvelope {
	data := map[string]any{
		"recipient": recipient,
		"content":   content,
	}
	if channel != "" {
		data["channel"] = channel
	}
	return eventbus.EventEnvelope{
		TenantID: "tenant-1",
		Data:     data,
	}
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
