package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type SendMessageCommand struct {
	TenantID  string
	Channel   string
	Recipient string
	Content   string
	// NotificationType groups this send in the delivery log. Empty means generic.
	NotificationType string
}

type SendMessageUseCase struct {
	repository     OutboxRepository
	provider       MessageProvider
	eventPublisher DeliveryEventPublisher
	deliveryLog    DeliveryLogRepository
	clock          Clock
	idGenerator    IDGenerator
	defaultTenant  string
}

func NewSendMessageUseCase(
	repository OutboxRepository,
	provider MessageProvider,
	eventPublisher DeliveryEventPublisher,
	deliveryLog DeliveryLogRepository,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
) *SendMessageUseCase {
	return &SendMessageUseCase{
		repository:     repository,
		provider:       provider,
		eventPublisher: eventPublisher,
		deliveryLog:    deliveryLog,
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

	status, sendErr := u.provider.Send(ctx, message.TenantID, message.Channel, message.Recipient, message.Content)
	if sendErr != nil {
		message = message.MarkFailed(u.clock.Now())
	} else {
		message = message.MarkSent(status, "", u.clock.Now())
	}

	if err := u.repository.Save(message); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("save delivered outbox message: %w", err)
	}

	u.recordDelivery(ctx, message, command.NotificationType, sendErr)

	if publishErr := u.publishDeliveryEvent(ctx, message, sendErr); publishErr != nil {
		// Transport event failures are not terminal for the message command.
	}

	return message, sendErr
}

// recordDelivery keeps the message as it was sent, so the log answers what the
// recipient read rather than what a template says now. A logging failure does
// not fail the send: the message already left.
func (u *SendMessageUseCase) recordDelivery(
	ctx context.Context,
	message domain.OutboxMessage,
	notificationType string,
	sendErr error,
) {
	if u.deliveryLog == nil {
		return
	}

	subject, body := splitSubjectAndBody(message.Content)
	failureReason := ""
	if sendErr != nil {
		failureReason = sendErr.Error()
	}

	entry, err := domain.NewDeliveryLogEntry(domain.DeliveryLogEntry{
		ID:                message.ID,
		TenantID:          message.TenantID,
		NotificationType:  notificationType,
		Channel:           message.Channel,
		Recipient:         message.Recipient,
		Subject:           subject,
		Content:           body,
		Status:            message.Status,
		ProviderMessageID: message.ProviderMessageID,
		FailureReason:     failureReason,
		CreatedAt:         message.CreatedAt,
		UpdatedAt:         message.UpdatedAt,
	})
	if err != nil {
		return
	}
	_ = u.deliveryLog.Append(ctx, entry)
}

// splitSubjectAndBody mirrors how the email provider reads content: a subject
// on the first line, then a blank line, then the body.
func splitSubjectAndBody(content string) (string, string) {
	normalized := strings.ReplaceAll(strings.TrimSpace(content), "\r\n", "\n")
	subject, body, found := strings.Cut(normalized, "\n\n")
	if found && strings.TrimSpace(subject) != "" && !strings.Contains(subject, "\n") {
		return strings.TrimSpace(subject), strings.TrimSpace(body)
	}
	return "", normalized
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
