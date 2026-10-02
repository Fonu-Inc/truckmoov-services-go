package application

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

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
