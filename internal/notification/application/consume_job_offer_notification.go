package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
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

type offerPreferenceReader interface {
	Get(ctx context.Context, tenantID string) (domain.NotificationPreferences, error)
}

// ConsumeJobOfferNotificationUseCase tells a tenant about an offer made to it,
// or about the answer to one it made. Dispatch publishes the offer on the
// subject of the tenant to be told but names no recipient, so the recipient is
// the tenant's own contact from auth, on the channels the tenant chose. An
// event that does carry one is delivered to it as given.
type ConsumeJobOfferNotificationUseCase struct {
	send        *SendMessageUseCase
	claims      EventClaimStore
	contacts    TenantContactReader
	preferences offerPreferenceReader
	accounts    instructionAccountFinder
	log         *slog.Logger
}

func NewConsumeJobOfferNotificationUseCase(send *SendMessageUseCase, claims EventClaimStore, log *slog.Logger) *ConsumeJobOfferNotificationUseCase {
	return &ConsumeJobOfferNotificationUseCase{send: send, claims: claims, log: log}
}

// WithContacts lets offers reach a tenant whose event names no recipient.
func (u *ConsumeJobOfferNotificationUseCase) WithContacts(contacts TenantContactReader) *ConsumeJobOfferNotificationUseCase {
	u.contacts = contacts
	return u
}

// WithChannels lets a tenant choose WhatsApp and SMS as well as email.
// WhatsApp needs the tenant's own number, which accounts answers for.
func (u *ConsumeJobOfferNotificationUseCase) WithChannels(preferences offerPreferenceReader, accounts instructionAccountFinder) *ConsumeJobOfferNotificationUseCase {
	u.preferences = preferences
	u.accounts = accounts
	return u
}

// offerTarget is one address to tell. A WhatsApp text outside the 24 hour
// window is refused by WhatsApp, so it carries an SMS number to fall back on.
type offerTarget struct {
	channel     string
	recipient   string
	smsFallback string
}

func (u *ConsumeJobOfferNotificationUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	if envelope.ID == "" {
		u.log.Warn("job_offer_without_id", "event", envelope.Type, "tenant", envelope.TenantID)
		return nil
	}
	recipient, _ := envelope.Data["recipient"].(string)
	if strings.TrimSpace(recipient) != "" {
		channel, _ := envelope.Data["channel"].(string)
		return u.deliverGiven(ctx, envelope, recipient, channel)
	}
	targets, err := u.targetsFor(ctx, envelope)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		u.log.Info("job_offer_notification_deferred", "event", envelope.Type, "offer_id", envelope.Data["offer_id"], "tenant", envelope.TenantID, "reason", "no_recipient")
		return nil
	}
	var failures error
	for _, target := range targets {
		failures = errors.Join(failures, u.deliverTarget(ctx, envelope, target))
	}
	return failures
}

func (u *ConsumeJobOfferNotificationUseCase) deliverGiven(ctx context.Context, envelope eventbus.EventEnvelope, recipient, channel string) error {
	if envelope.BusSignature == "" {
		u.log.Warn("job_offer_unsigned_recipient_accepted", "event", envelope.Type, "tenant", envelope.TenantID)
	}
	if strings.TrimSpace(channel) == "" {
		channel = "whatsapp"
	}
	return u.sendOffer(ctx, envelope, channel, recipient)
}

func (u *ConsumeJobOfferNotificationUseCase) deliverTarget(ctx context.Context, envelope eventbus.EventEnvelope, target offerTarget) error {
	err := u.sendOffer(ctx, envelope, target.channel, target.recipient)
	if err == nil || target.smsFallback == "" {
		return err
	}
	u.log.Warn("job_offer_whatsapp_failed_trying_sms", "tenant", envelope.TenantID, "offer_id", envelope.Data["offer_id"], "error", err)
	return u.sendOffer(ctx, envelope, "sms", target.smsFallback)
}

// sendOffer sends once per event, address and channel. A wallet refusal is
// final, so it is logged and not retried; the other channels still go out.
func (u *ConsumeJobOfferNotificationUseCase) sendOffer(ctx context.Context, envelope eventbus.EventEnvelope, channel, recipient string) error {
	content := jobOfferContent(envelope, channel)
	key := strings.Join([]string{"job_offer", envelope.ID, recipient, channel}, "|")
	_, err := sendOnce(ctx, u.claims, key, func() error {
		_, sendErr := u.send.Execute(ctx, SendMessageCommand{TenantID: envelope.TenantID, Channel: channel, Recipient: recipient, Content: content, NotificationType: "job_offer"})
		return sendErr
	})
	if err != nil && isTerminalChargeRefusal(err) {
		u.log.Warn("job_offer_notification_refused", "tenant", envelope.TenantID, "channel", channel, "error", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("send job offer notification: %w", err)
	}
	return nil
}

// targetsFor lists where the tenant's own contact is told, following the
// tenant's choice of channels. Email is the default.
func (u *ConsumeJobOfferNotificationUseCase) targetsFor(ctx context.Context, envelope eventbus.EventEnvelope) ([]offerTarget, error) {
	if u.contacts == nil || strings.TrimSpace(envelope.TenantID) == "" {
		return nil, nil
	}
	contact, found, err := u.contacts.ContactFor(ctx, envelope.TenantID)
	if err != nil {
		return nil, fmt.Errorf("read contact for tenant %s: %w", envelope.TenantID, err)
	}
	if !found {
		return nil, nil
	}
	preferences, err := u.preferencesFor(ctx, envelope.TenantID)
	if err != nil {
		return nil, err
	}
	hasNumber, err := u.hasWhatsAppNumber(ctx, envelope.TenantID, preferences)
	if err != nil {
		return nil, err
	}
	return offerTargets(contact, preferences, hasNumber), nil
}

func (u *ConsumeJobOfferNotificationUseCase) preferencesFor(ctx context.Context, tenantID string) (domain.NotificationPreferences, error) {
	if u.preferences == nil {
		return domain.DefaultNotificationPreferences(tenantID), nil
	}
	preferences, err := u.preferences.Get(ctx, tenantID)
	if err != nil {
		return domain.NotificationPreferences{}, fmt.Errorf("read notification preferences for tenant %s: %w", tenantID, err)
	}
	return preferences, nil
}

func (u *ConsumeJobOfferNotificationUseCase) hasWhatsAppNumber(ctx context.Context, tenantID string, preferences domain.NotificationPreferences) (bool, error) {
	if !preferences.WhatsApp || u.accounts == nil {
		return false, nil
	}
	_, found, err := u.accounts.FindAccountByTenantID(ctx, tenantID)
	if err != nil {
		return false, fmt.Errorf("read whatsapp account for %s: %w", tenantID, err)
	}
	return found, nil
}

func offerTargets(contact TenantContact, preferences domain.NotificationPreferences, hasWhatsAppNumber bool) []offerTarget {
	email := strings.TrimSpace(contact.Email)
	phone := strings.TrimSpace(contact.Phone)
	targets := []offerTarget{}
	if preferences.Email && email != "" {
		targets = append(targets, offerTarget{channel: "email", recipient: email})
	}
	if phone == "" {
		return targets
	}
	whatsAppUsable := preferences.WhatsApp && hasWhatsAppNumber
	if whatsAppUsable {
		target := offerTarget{channel: "whatsapp", recipient: phone}
		if !preferences.SMS {
			target.smsFallback = phone
		}
		targets = append(targets, target)
	}
	if preferences.SMS || (preferences.WhatsApp && !hasWhatsAppNumber) {
		targets = append(targets, offerTarget{channel: "sms", recipient: phone})
	}
	return targets
}

func jobOfferContent(envelope eventbus.EventEnvelope, channel string) string {
	offerID, _ := envelope.Data["offer_id"].(string)
	status, _ := envelope.Data["status"].(string)
	body := fmt.Sprintf("Job offer %s status: %s", offerID, status)
	subject := "Job offer update"
	if envelope.Type == "job.offer.created" {
		subject = "You have a new job offer"
		body = fmt.Sprintf("You have a new job offer (%s)%s. Open FleetPro to review it.", offerID, offerRateAndExpiry(envelope.Data))
	} else if status != "" {
		body = fmt.Sprintf("Your job offer %s was %s. Open FleetPro to see the job.", offerID, status)
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
