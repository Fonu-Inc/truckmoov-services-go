package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

// ConsumeNotificationUseCase handles tm.*.notification.send events from the bus.
type ConsumeNotificationUseCase struct {
	send *SendMessageUseCase
	log  *slog.Logger
}

func NewConsumeNotificationUseCase(send *SendMessageUseCase, log *slog.Logger) *ConsumeNotificationUseCase {
	return &ConsumeNotificationUseCase{send: send, log: log}
}

func (u *ConsumeNotificationUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
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
	})
	if err != nil {
		return fmt.Errorf("send notification to %s: %w", recipient, err)
	}
	return nil
}
