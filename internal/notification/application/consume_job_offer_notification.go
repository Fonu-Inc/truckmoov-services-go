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
	send   *SendMessageUseCase
	claims EventClaimStore
	log    *slog.Logger
}

func NewConsumeJobOfferNotificationUseCase(send *SendMessageUseCase, claims EventClaimStore, log *slog.Logger) *ConsumeJobOfferNotificationUseCase {
	return &ConsumeJobOfferNotificationUseCase{send: send, claims: claims, log: log}
}

func (u *ConsumeJobOfferNotificationUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	recipient, _ := envelope.Data["recipient"].(string)
	channel, _ := envelope.Data["channel"].(string)
	if strings.TrimSpace(recipient) == "" {
		u.log.Info("job_offer_notification_deferred", "event", envelope.Type, "offer_id", envelope.Data["offer_id"], "tenant", envelope.TenantID, "reason", "recipient_not_in_event")
		return nil
	}
	if envelope.ID == "" {
		u.log.Warn("job_offer_without_id", "event", envelope.Type, "tenant", envelope.TenantID)
		return nil
	}
	if envelope.BusSignature == "" {
		u.log.Warn("job_offer_unsigned_recipient_accepted", "event", envelope.Type, "tenant", envelope.TenantID)
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
	key := strings.Join([]string{"job_offer", envelope.ID, recipient, channel}, "|")
	_, err := sendOnce(ctx, u.claims, key, func() error {
		_, sendErr := u.send.Execute(ctx, SendMessageCommand{TenantID: envelope.TenantID, Channel: channel, Recipient: recipient, Content: content, NotificationType: "job_offer"})
		return sendErr
	})
	if err != nil {
		return fmt.Errorf("send job offer notification: %w", err)
	}
	return nil
}
