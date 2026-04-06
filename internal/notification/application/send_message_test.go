package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeOutboxRepository struct {
	data              map[string]domain.OutboxMessage
	providerMessageID map[string]string
}

func newFakeOutboxRepository() *fakeOutboxRepository {
	return &fakeOutboxRepository{
		data:              make(map[string]domain.OutboxMessage),
		providerMessageID: make(map[string]string),
	}
}

func (r *fakeOutboxRepository) Save(message domain.OutboxMessage) error {
	r.data[message.ID] = message
	if message.ProviderMessageID != "" {
		r.providerMessageID[message.ProviderMessageID] = message.ID
	}
	return nil
}

func (r *fakeOutboxRepository) FindByID(id string) (domain.OutboxMessage, bool) {
	message, ok := r.data[id]
	return message, ok
}

func (r *fakeOutboxRepository) FindByProviderMessageID(providerMessageID string) (domain.OutboxMessage, bool) {
	id, ok := r.providerMessageID[providerMessageID]
	if !ok {
		return domain.OutboxMessage{}, false
	}
	message, ok := r.data[id]
	return message, ok
}

type fakeMessageProvider struct {
	status string
	err    error
}

func (p *fakeMessageProvider) Send(_ context.Context, _, _, _ string) (string, error) {
	return p.status, p.err
}

type fakeDeliveryPublisher struct{ published int }

func (p *fakeDeliveryPublisher) PublishDeliveryUpdated(_ context.Context, _ domain.MessageDeliveryUpdated) error {
	p.published++
	return nil
}

type fixedNotificationClock struct{ now time.Time }

func (f fixedNotificationClock) Now() time.Time { return f.now }

type fixedNotificationIDGenerator struct{ id string }

func (f fixedNotificationIDGenerator) New(prefix string) string { return prefix + "-" + f.id }

func TestSendMessageUseCase_Execute_Success(t *testing.T) {
	repository := newFakeOutboxRepository()
	publisher := &fakeDeliveryPublisher{}
	useCase := NewSendMessageUseCase(
		repository,
		&fakeMessageProvider{status: "sent"},
		publisher,
		fixedNotificationClock{now: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "1"},
		"default",
	)

	message, err := useCase.Execute(context.Background(), SendMessageCommand{
		Recipient: "+23400000000",
		Content:   "hello",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if message.Status != "sent" {
		t.Fatalf("expected sent, got %s", message.Status)
	}
	if publisher.published != 1 {
		t.Fatalf("expected one event publication")
	}
}

func TestRetryMessageUseCase_Execute_NotFound(t *testing.T) {
	useCase := NewRetryMessageUseCase(
		newFakeOutboxRepository(),
		&fakeMessageProvider{status: "sent"},
		&fakeDeliveryPublisher{},
		fixedNotificationClock{now: time.Now()},
		fixedNotificationIDGenerator{id: "2"},
	)

	_, err := useCase.Execute(context.Background(), "missing")
	if !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("expected ErrMessageNotFound, got %v", err)
	}
}
