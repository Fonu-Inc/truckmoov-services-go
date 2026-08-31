package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

// ConsumeJobOfferNotificationUseCase is the notification-service boundary for
// dispatch offer events. Dispatch currently publishes tenant/realtime data but
// no phone/email recipient, so those events are intentionally acknowledged and
// logged until a recipient is supplied by the dispatch contract. This avoids
// guessing a tenant administrator or sending to an invalid address.
type ConsumeJobOfferNotificationUseCase struct {
	send *SendMessageUseCase
	log  *slog.Logger
}

func NewConsumeJobOfferNotificationUseCase(send *SendMessageUseCase, log *slog.Logger) *ConsumeJobOfferNotificationUseCase {
	return &ConsumeJobOfferNotificationUseCase{send: send, log: log}
}

func (u *ConsumeJobOfferNotificationUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	recipient, _ := envelope.Data["recipient"].(string)
	channel, _ := envelope.Data["channel"].(string)
	if strings.TrimSpace(recipient) == "" {
		u.log.Info("job_offer_notification_deferred", "event", envelope.Type, "offer_id", envelope.Data["offer_id"], "tenant", envelope.TenantID, "reason", "recipient_not_in_event")
		return nil
	}
	if strings.TrimSpace(channel) == "" {
		channel = "whatsapp"
	}
	status, _ := envelope.Data["status"].(string)
	offerID, _ := envelope.Data["offer_id"].(string)
	content := fmt.Sprintf("Job offer %s status: %s", offerID, status)
	if envelope.Type == "job.offer.created" {
		content = fmt.Sprintf("You have a new job offer (%s). Open TruckMoov to review it.", offerID)
	}
	if _, err := u.send.Execute(ctx, SendMessageCommand{TenantID: envelope.TenantID, Channel: channel, Recipient: recipient, Content: content, NotificationType: "job_offer"}); err != nil {
		return fmt.Errorf("send job offer notification: %w", err)
	}
	return nil
}
