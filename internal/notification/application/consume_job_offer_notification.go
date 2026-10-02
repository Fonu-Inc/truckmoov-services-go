package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

// TenantContact is where an organisation can be reached.
type TenantContact struct {
	Email string
	Phone string
}

// TenantContactReader finds who to tell at a tenant. Auth owns the users, so
// this asks rather than keeping a copy.
type TenantContactReader interface {
	ContactFor(ctx context.Context, tenantID string) (TenantContact, bool, error)
}

// ConsumeJobOfferNotificationUseCase tells a tenant about an offer made to it,
// or about the answer to one it made. Dispatch publishes the offer on the
// subject of the tenant to be told but names no recipient, so the recipient is
// the tenant's own contact from auth. An event that does carry one is
// delivered to it as given.
type ConsumeJobOfferNotificationUseCase struct {
	send     *SendMessageUseCase
	claims   EventClaimStore
	contacts TenantContactReader
	log      *slog.Logger
}

func NewConsumeJobOfferNotificationUseCase(send *SendMessageUseCase, claims EventClaimStore, log *slog.Logger) *ConsumeJobOfferNotificationUseCase {
	return &ConsumeJobOfferNotificationUseCase{send: send, claims: claims, log: log}
}

// WithContacts lets offers reach a tenant whose event names no recipient.
func (u *ConsumeJobOfferNotificationUseCase) WithContacts(contacts TenantContactReader) *ConsumeJobOfferNotificationUseCase {
	u.contacts = contacts
	return u
}

func (u *ConsumeJobOfferNotificationUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	recipient, _ := envelope.Data["recipient"].(string)
	channel, _ := envelope.Data["channel"].(string)
	if strings.TrimSpace(recipient) == "" {
		var resolved bool
		var err error
		recipient, resolved, err = u.resolveRecipient(ctx, envelope)
		if err != nil {
			return err
		}
		if !resolved {
			u.log.Info("job_offer_notification_deferred", "event", envelope.Type, "offer_id", envelope.Data["offer_id"], "tenant", envelope.TenantID, "reason", "no_recipient")
			return nil
		}
		channel = "email"
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
	content := jobOfferContent(envelope, channel)
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

// resolveRecipient finds the tenant's own contact. Only an email is used: a
// WhatsApp text to a number the tenant has not messaged recently is refused by
// WhatsApp, so it would leave the offer unannounced.
func (u *ConsumeJobOfferNotificationUseCase) resolveRecipient(ctx context.Context, envelope eventbus.EventEnvelope) (string, bool, error) {
	if u.contacts == nil || strings.TrimSpace(envelope.TenantID) == "" {
		return "", false, nil
	}
	contact, found, err := u.contacts.ContactFor(ctx, envelope.TenantID)
	if err != nil {
		return "", false, fmt.Errorf("read contact for tenant %s: %w", envelope.TenantID, err)
	}
	email := strings.TrimSpace(contact.Email)
	return email, found && email != "", nil
}

func jobOfferContent(envelope eventbus.EventEnvelope, channel string) string {
	offerID, _ := envelope.Data["offer_id"].(string)
	status, _ := envelope.Data["status"].(string)
	body := fmt.Sprintf("Job offer %s status: %s", offerID, status)
	subject := "Job offer update"
	if envelope.Type == "job.offer.created" {
		subject = "You have a new job offer"
		body = fmt.Sprintf("You have a new job offer (%s)%s. Open TruckMoov to review it.", offerID, offerRateAndExpiry(envelope.Data))
	} else if status != "" {
		body = fmt.Sprintf("Your job offer %s was %s. Open TruckMoov to see the job.", offerID, status)
	}
	if channel == "email" {
		return subject + "\n\n" + body
	}
	return body
}

func offerRateAndExpiry(data map[string]any) string {
	detail := ""
	if rate, ok := data["offered_rate_ngn"].(float64); ok && rate > 0 {
		detail += fmt.Sprintf(" at NGN %.0f", rate)
	}
	if expires, ok := data["expires_at"].(string); ok && expires != "" {
		detail += ", open until " + expires
	}
	return detail
}
