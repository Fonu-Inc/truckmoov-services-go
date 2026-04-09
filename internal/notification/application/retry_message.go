package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

var ErrMessageNotFound = errors.New("message not found")

type RetryMessageUseCase struct {
	repository     OutboxRepository
	provider       MessageProvider
	eventPublisher DeliveryEventPublisher
	clock          Clock
	idGenerator    IDGenerator
}

func NewRetryMessageUseCase(
	repository OutboxRepository,
	provider MessageProvider,
	eventPublisher DeliveryEventPublisher,
	clock Clock,
	idGenerator IDGenerator,
) *RetryMessageUseCase {
	return &RetryMessageUseCase{
		repository:     repository,
		provider:       provider,
		eventPublisher: eventPublisher,
		clock:          clock,
		idGenerator:    idGenerator,
	}
}

func (u *RetryMessageUseCase) Execute(ctx context.Context, id string) (domain.OutboxMessage, error) {
	message, ok := u.repository.FindByID(id)
	if !ok {
		return domain.OutboxMessage{}, ErrMessageNotFound
	}

	status, sendErr := u.provider.Send(ctx, message.TenantID, message.Channel, message.Recipient, message.Content)
	if sendErr != nil {
		message = message.MarkFailed(u.clock.Now())
	} else {
		message = message.MarkRetry(status, "", u.clock.Now())
	}

	if err := u.repository.Save(message); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("save retried outbox message: %w", err)
	}

	if publishErr := u.publishDeliveryEvent(ctx, message, sendErr); publishErr != nil {
		// Event publish failures should not hide provider retry outcome.
	}

	return message, sendErr
}

func (u *RetryMessageUseCase) publishDeliveryEvent(
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
