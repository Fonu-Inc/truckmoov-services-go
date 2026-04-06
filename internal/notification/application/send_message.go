package application

import (
	"context"
	"fmt"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type SendMessageCommand struct {
	TenantID  string
	Channel   string
	Recipient string
	Content   string
}

type SendMessageUseCase struct {
	repository     OutboxRepository
	provider       MessageProvider
	eventPublisher DeliveryEventPublisher
	clock          Clock
	idGenerator    IDGenerator
	defaultTenant  string
}

func NewSendMessageUseCase(
	repository OutboxRepository,
	provider MessageProvider,
	eventPublisher DeliveryEventPublisher,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
) *SendMessageUseCase {
	return &SendMessageUseCase{
		repository:     repository,
		provider:       provider,
		eventPublisher: eventPublisher,
		clock:          clock,
		idGenerator:    idGenerator,
		defaultTenant:  defaultTenant,
	}
}

func (u *SendMessageUseCase) Execute(ctx context.Context, command SendMessageCommand) (domain.OutboxMessage, error) {
	tenantID := command.TenantID
	if tenantID == "" {
		tenantID = u.defaultTenant
	}

	channel := command.Channel
	if channel == "" {
		channel = "whatsapp"
	}

	now := u.clock.Now()
	message, err := domain.NewOutboxMessage(domain.OutboxMessage{
		ID:        u.idGenerator.New("msg"),
		TenantID:  tenantID,
		Channel:   channel,
		Recipient: command.Recipient,
		Content:   command.Content,
		Status:    domain.StatusQueued,
		Retries:   0,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("build outbox message: %w", err)
	}

	if err := u.repository.Save(message); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("save queued outbox message: %w", err)
	}

	status, sendErr := u.provider.Send(ctx, message.Channel, message.Recipient, message.Content)
	if sendErr != nil {
		message = message.MarkFailed(u.clock.Now())
	} else {
		message = message.MarkSent(status, "", u.clock.Now())
	}

	if err := u.repository.Save(message); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("save delivered outbox message: %w", err)
	}

	if publishErr := u.publishDeliveryEvent(ctx, message, sendErr); publishErr != nil {
		// Transport event failures are not terminal for the message command.
	}

	return message, sendErr
}

func (u *SendMessageUseCase) publishDeliveryEvent(
	ctx context.Context,
	message domain.OutboxMessage,
	sendErr error,
) error {
	event, err := domain.NewMessageDeliveryUpdated(domain.MessageDeliveryUpdated{
		EventID:    u.idGenerator.New("evt"),
		OccurredAt: u.clock.Now(),
		TenantID:   message.TenantID,
		MessageID:  message.ID,
		Channel:    message.Channel,
		Status:     message.Status,
	})
	if err != nil {
		return err
	}
	if sendErr != nil {
		event.ErrorText = sendErr.Error()
	}

	return u.eventPublisher.PublishDeliveryUpdated(ctx, event)
}
