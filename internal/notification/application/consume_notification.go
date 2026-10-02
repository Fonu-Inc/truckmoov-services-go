package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

// ConsumeNotificationUseCase handles tm.*.notification.send events from the bus.
type ConsumeNotificationUseCase struct {
	send          *SendMessageUseCase
	signingSecret string
	log           *slog.Logger
}

func NewConsumeNotificationUseCase(send *SendMessageUseCase, signingSecret string, log *slog.Logger) *ConsumeNotificationUseCase {
	return &ConsumeNotificationUseCase{send: send, signingSecret: signingSecret, log: log}
}

func (u *ConsumeNotificationUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	if err := verifyNotification(u.signingSecret, envelope); err != nil {
		u.log.Error("notification_send_rejected", "tenant", envelope.TenantID, "subject", envelope.Subject, "event", envelope.ID, "error", err)
		return nil
	}

	recipient, _ := envelope.Data["recipient"].(string)
	content, _ := envelope.Data["content"].(string)
	channel, _ := envelope.Data["channel"].(string)
	notificationType, _ := envelope.Data["notification_type"].(string)

	if recipient == "" || content == "" {
		u.log.Warn("notification_send_invalid", "tenant", envelope.TenantID)
		return nil
	}

	if channel == "" {
		channel = "whatsapp"
	}

	_, err := u.send.Execute(ctx, SendMessageCommand{
		TenantID:         envelope.TenantID,
		Channel:          channel,
		Recipient:        recipient,
		Content:          content,
		NotificationType: notificationType,
		MessageID:        stableMessageID(envelope.ID),
	})
	if isTerminalChargeRefusal(err) {
		u.log.Warn("notification_charge_refused", "tenant", envelope.TenantID, "event", envelope.ID, "error", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("send notification to %s: %w", recipient, err)
	}
	return nil
}

func stableMessageID(envelopeID string) string {
	if envelopeID == "" {
		return ""
	}
	return "msg-" + envelopeID
}

func stableConversationMessageID(envelopeID string) string {
	if envelopeID == "" {
		return ""
	}
	return "wam-" + envelopeID
}

// A refused charge never succeeds on redelivery, and the message is already
// recorded as failed, so it is acked rather than looped.
func isTerminalChargeRefusal(err error) bool {
	return errors.Is(err, ErrInsufficientFunds) || errors.Is(err, ErrChannelDisabled)
}
