package application

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

func TestConsumeJobOfferNotificationDefersWithoutRecipient(t *testing.T) {
	uc := NewConsumeJobOfferNotificationUseCase(nil, newFakeEventClaims(), slog.Default())
	err := uc.Handle(context.Background(), eventbus.EventEnvelope{Type: "job.offer.created", TenantID: "tenant-a", Data: map[string]any{"offer_id": "offer-1"}})
	if err != nil {
		t.Fatalf("expected recipient-less event to be acknowledged, got %v", err)
	}
}

type offerSend struct{ tenantID, channel, recipient, content string }

type offerRecordingProvider struct{ sent []offerSend }

func (p *offerRecordingProvider) Send(_ context.Context, tenantID, channel, recipient, content string) (string, error) {
	p.sent = append(p.sent, offerSend{tenantID, channel, recipient, content})
	return "sent", nil
}

type stubContacts struct {
	contact TenantContact
	found   bool
	err     error
	asked   []string
}

func (s *stubContacts) ContactFor(_ context.Context, tenantID string) (TenantContact, bool, error) {
	s.asked = append(s.asked, tenantID)
	return s.contact, s.found, s.err
}

func offerConsumer(provider *offerRecordingProvider, contacts *stubContacts) *ConsumeJobOfferNotificationUseCase {
	clock := fixedNotificationClock{now: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)}
	send := NewSendMessageUseCase(newFakeOutboxRepository(), provider, &fakeDeliveryPublisher{}, nil, nil, clock, fixedNotificationIDGenerator{id: "1"}, "default")
	return NewConsumeJobOfferNotificationUseCase(send, newFakeEventClaims(), slog.Default()).WithContacts(contacts)
}

func offerEvent(eventType, tenantID, status string) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{
		ID: "evt-1", Type: eventType, TenantID: tenantID,
		Data: map[string]any{"offer_id": "offer-1", "status": status, "offered_rate_ngn": 15000.0, "expires_at": "2026-03-02T10:00:00Z"},
	}
}

func TestJobOfferCreatedEmailsTheTargetTenantsContact(t *testing.T) {
	provider := &offerRecordingProvider{}
	contacts := &stubContacts{contact: TenantContact{Email: "ops@carrier.com"}, found: true}

	if err := offerConsumer(provider, contacts).Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(provider.sent) != 1 {
		t.Fatalf("expected one message, got %+v", provider.sent)
	}
	sent := provider.sent[0]
	if sent.tenantID != "carrier" || sent.channel != "email" || sent.recipient != "ops@carrier.com" {
		t.Fatalf("unexpected send %+v", sent)
	}
	if !strings.HasPrefix(sent.content, "You have a new job offer\n\n") || !strings.Contains(sent.content, "NGN 15000") || !strings.Contains(sent.content, "offer-1") {
		t.Fatalf("unexpected content %q", sent.content)
	}
	if len(contacts.asked) != 1 || contacts.asked[0] != "carrier" {
		t.Fatalf("expected the contact of the tenant on the subject, got %v", contacts.asked)
	}
}

func TestJobOfferResponseTellsTheOfferingTenantTheAnswer(t *testing.T) {
	provider := &offerRecordingProvider{}
	contacts := &stubContacts{contact: TenantContact{Email: "dispatch@owner.com"}, found: true}

	if err := offerConsumer(provider, contacts).Handle(context.Background(), offerEvent("job.offer.responded", "owner", "accepted")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(provider.sent) != 1 || provider.sent[0].recipient != "dispatch@owner.com" || !strings.Contains(provider.sent[0].content, "was accepted") {
		t.Fatalf("unexpected sends %+v", provider.sent)
	}
}

func TestJobOfferNotificationIsDeferredWhenTheTenantHasNoContact(t *testing.T) {
	provider := &offerRecordingProvider{}

	for _, contacts := range []*stubContacts{{found: false}, {found: true, contact: TenantContact{Phone: "+2348012345678"}}} {
		if err := offerConsumer(provider, contacts).Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending")); err != nil {
			t.Fatalf("expected the event to be acknowledged, got %v", err)
		}
	}
	if len(provider.sent) != 0 {
		t.Fatalf("nothing may be sent without an email, got %+v", provider.sent)
	}
}

func TestJobOfferNotificationIsRetriedWhenTheContactCannotBeRead(t *testing.T) {
	err := offerConsumer(&offerRecordingProvider{}, &stubContacts{err: errors.New("auth down")}).
		Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if err == nil {
		t.Fatal("expected the error to be returned so the event is redelivered")
	}
}

func TestJobOfferReplayEmailsOnce(t *testing.T) {
	provider := &offerRecordingProvider{}
	consumer := offerConsumer(provider, &stubContacts{contact: TenantContact{Email: "ops@carrier.com"}, found: true})

	_ = consumer.Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))
	_ = consumer.Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if len(provider.sent) != 1 {
		t.Fatalf("expected one email, got %d", len(provider.sent))
	}
}

type channelProvider struct {
	sent []offerSend
	fail map[string]error
}

func (p *channelProvider) Send(_ context.Context, tenantID, channel, recipient, content string) (string, error) {
	if err := p.fail[channel]; err != nil {
		return "", err
	}
	p.sent = append(p.sent, offerSend{tenantID, channel, recipient, content})
	return "sent", nil
}

func (p *channelProvider) channels() []string {
	channels := []string{}
	for _, send := range p.sent {
		channels = append(channels, send.channel)
	}
	return channels
}

type stubPreferences struct {
	preferences domain.NotificationPreferences
	err         error
}

func (s stubPreferences) Get(_ context.Context, tenantID string) (domain.NotificationPreferences, error) {
	preferences := s.preferences
	preferences.TenantID = tenantID
	return preferences, s.err
}

func offerConsumerOnChannels(provider MessageProvider, preferences stubPreferences, hasNumber bool) *ConsumeJobOfferNotificationUseCase {
	clock := fixedNotificationClock{now: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)}
	send := NewSendMessageUseCase(newFakeOutboxRepository(), provider, &fakeDeliveryPublisher{}, nil, nil, clock, fixedNotificationIDGenerator{id: "1"}, "default")
	contacts := &stubContacts{contact: TenantContact{Email: "ops@carrier.com", Phone: "+2348012345678"}, found: true}
	return NewConsumeJobOfferNotificationUseCase(send, newFakeEventClaims(), slog.Default()).
		WithContacts(contacts).
		WithChannels(preferences, stubAccountFinder{found: hasNumber})
}

func TestJobOfferDefaultsToEmailOnly(t *testing.T) {
	provider := &channelProvider{}

	err := offerConsumerOnChannels(provider, stubPreferences{preferences: domain.DefaultNotificationPreferences("carrier")}, true).
		Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if err != nil || len(provider.sent) != 1 || provider.sent[0].channel != "email" {
		t.Fatalf("expected only an email, got %+v err=%v", provider.sent, err)
	}
}

func TestJobOfferReachesACarrierNotInTheAppOnWhatsApp(t *testing.T) {
	provider := &channelProvider{}
	preferences := stubPreferences{preferences: domain.NotificationPreferences{Email: true, WhatsApp: true}}

	err := offerConsumerOnChannels(provider, preferences, true).Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if err != nil || len(provider.sent) != 2 {
		t.Fatalf("expected email and whatsapp, got %+v err=%v", provider.sent, err)
	}
	if provider.sent[1].channel != "whatsapp" || provider.sent[1].recipient != "+2348012345678" || provider.sent[1].tenantID != "carrier" {
		t.Fatalf("unexpected whatsapp send %+v", provider.sent[1])
	}
	if strings.Contains(provider.sent[1].content, "\n\n") {
		t.Fatalf("a whatsapp text carries no email subject, got %q", provider.sent[1].content)
	}
}

func TestJobOfferFallsBackToSMSWhenWhatsAppFails(t *testing.T) {
	provider := &channelProvider{fail: map[string]error{"whatsapp": errors.New("outside the 24 hour window")}}
	preferences := stubPreferences{preferences: domain.NotificationPreferences{WhatsApp: true}}

	err := offerConsumerOnChannels(provider, preferences, true).Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if err != nil {
		t.Fatalf("a delivered fallback is not an error: %v", err)
	}
	if got := provider.channels(); len(got) != 1 || got[0] != "sms" {
		t.Fatalf("expected the sms fallback, got %v", got)
	}
}

func TestJobOfferUsesSMSWhenTheTenantHasNoWhatsAppNumber(t *testing.T) {
	provider := &channelProvider{}
	preferences := stubPreferences{preferences: domain.NotificationPreferences{WhatsApp: true}}

	_ = offerConsumerOnChannels(provider, preferences, false).Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if got := provider.channels(); len(got) != 1 || got[0] != "sms" {
		t.Fatalf("expected sms, got %v", got)
	}
}

func TestJobOfferSMSChoiceSendsOnlySMS(t *testing.T) {
	provider := &channelProvider{}
	preferences := stubPreferences{preferences: domain.NotificationPreferences{SMS: true}}

	_ = offerConsumerOnChannels(provider, preferences, true).Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if got := provider.channels(); len(got) != 1 || got[0] != "sms" {
		t.Fatalf("expected one sms, got %v", got)
	}
}

func TestJobOfferWhatsAppAndSMSBothChosenSendsBoth(t *testing.T) {
	provider := &channelProvider{}
	preferences := stubPreferences{preferences: domain.NotificationPreferences{WhatsApp: true, SMS: true}}

	_ = offerConsumerOnChannels(provider, preferences, true).Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if got := provider.channels(); len(got) != 2 || got[0] != "whatsapp" || got[1] != "sms" {
		t.Fatalf("expected whatsapp then sms, got %v", got)
	}
}

func TestJobOfferChannelsAreSentOncePerEventOnReplay(t *testing.T) {
	provider := &channelProvider{}
	consumer := offerConsumerOnChannels(provider, stubPreferences{preferences: domain.NotificationPreferences{Email: true, SMS: true}}, true)

	_ = consumer.Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))
	_ = consumer.Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if len(provider.sent) != 2 {
		t.Fatalf("expected one email and one sms, got %v", provider.channels())
	}
}

func TestJobOfferPreferencesFailureIsRetried(t *testing.T) {
	err := offerConsumerOnChannels(&channelProvider{}, stubPreferences{err: errors.New("db down")}, true).
		Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if err == nil {
		t.Fatal("expected the error so the event is redelivered")
	}
}

func TestJobOfferRefusedWalletOnWhatsAppDoesNotLoopTheEvent(t *testing.T) {
	provider := &channelProvider{}
	clock := fixedNotificationClock{now: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)}
	send := NewSendMessageUseCase(newFakeOutboxRepository(), provider, &fakeDeliveryPublisher{}, nil, &campaignLimitedCharger{allowed: 0}, clock, fixedNotificationIDGenerator{id: "1"}, "default")
	consumer := NewConsumeJobOfferNotificationUseCase(send, newFakeEventClaims(), slog.Default()).
		WithContacts(&stubContacts{contact: TenantContact{Email: "ops@carrier.com", Phone: "+2348012345678"}, found: true}).
		WithChannels(stubPreferences{preferences: domain.NotificationPreferences{Email: true, WhatsApp: true}}, stubAccountFinder{found: true})

	err := consumer.Handle(context.Background(), offerEvent("job.offer.created", "carrier", "pending"))

	if err != nil {
		t.Fatalf("a refused wallet is final, got %v", err)
	}
	if got := provider.channels(); len(got) != 1 || got[0] != "email" {
		t.Fatalf("the email still goes out, got %v", got)
	}
}
