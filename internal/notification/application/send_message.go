package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

type SendMessageCommand struct {
	TenantID  string
	Channel   string
	Recipient string
	Content   string
	// NotificationType groups this send in the delivery log. Empty means generic.
	NotificationType string
	// MessageID makes the message id, and so the charge reference, stable
	// across redeliveries. Empty means a fresh id.
	MessageID string
}

type SendMessageUseCase struct {
	repository     OutboxRepository
	provider       MessageProvider
	eventPublisher DeliveryEventPublisher
	deliveryLog    DeliveryLogRepository
	usageCharger   UsageCharger
	clock          Clock
	idGenerator    IDGenerator
	defaultTenant  string
}

func NewSendMessageUseCase(
	repository OutboxRepository,
	provider MessageProvider,
	eventPublisher DeliveryEventPublisher,
	deliveryLog DeliveryLogRepository,
	usageCharger UsageCharger,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
) *SendMessageUseCase {
	return &SendMessageUseCase{
		repository:     repository,
		provider:       provider,
		eventPublisher: eventPublisher,
		deliveryLog:    deliveryLog,
		usageCharger:   usageCharger,
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

	messageID := command.MessageID
	if messageID == "" {
		messageID = u.idGenerator.New("msg")
	}

	now := u.clock.Now()
	message, err := domain.NewOutboxMessage(domain.OutboxMessage{
		ID:        messageID,
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

	// Charged before the provider is called, never after. A prepaid wallet has
	// to be able to refuse, and once the provider has accepted the message the
	// money is spent whatever the balance says.
	if err := u.chargeForSend(ctx, message); err != nil {
		err = fmt.Errorf("usage charge failed: %w", err)
		message = message.MarkFailed(u.clock.Now())
		if saveErr := u.repository.Save(message); saveErr != nil {
			return domain.OutboxMessage{}, fmt.Errorf("save refused outbox message: %w", saveErr)
		}
		u.recordDelivery(ctx, message, command.NotificationType, err)
		return message, err
	}

	sent, sendErr := sendViaProvider(ctx, u.provider, message.TenantID, message.Channel, message.Recipient, message.Content)
	if sendErr != nil {
		message = message.MarkFailed(u.clock.Now())
	} else {
		message = message.MarkSent(sent.Status, sent.ProviderMessageID, u.clock.Now())
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

// chargeForSend debits the tenant's wallet for one message.
//
// A channel with no rate is not charged rather than being refused: email rides
// on the tenant's own mail server and costs the platform nothing. A missing
// charger means charging is not configured in this deployment, which must not
// silently stop every send. The system tenant is the platform itself, so its
// sends are logged but never debited.
func (u *SendMessageUseCase) chargeForSend(ctx context.Context, message domain.OutboxMessage) error {
	return chargeOutboxMessage(ctx, u.usageCharger, message)
}

func chargeOutboxMessage(ctx context.Context, charger UsageCharger, message domain.OutboxMessage) error {
	if charger == nil || !IsBillable(message.Channel) || message.TenantID == identity.SystemTenantID {
		return nil
	}

	return charger.Charge(ctx, UsageCharge{
		TenantID: message.TenantID,
		Channel:  message.Channel,
		Quantity: 1,
		Reason:   message.Channel + " message to " + message.Recipient,
		// The message id, so a retry of the same message charges once.
		Reference: message.ID,
	})
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
