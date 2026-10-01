package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

var inboxNow = time.Unix(1_800_000_000, 0).UTC()

type platformRepo struct {
	*fakeWhatsAppConversationRepository
}

func (r *platformRepo) SaveMessage(ctx context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error) {
	if message.ClientMessageID != "" {
		if existing, ok, _ := r.FindMessageByClientID(ctx, message.TenantID, message.ClientMessageID); ok && existing.ID != message.ID {
			return domain.WhatsAppConversationMessage{}, errors.New("duplicate client message id")
		}
	}
	return r.fakeWhatsAppConversationRepository.SaveMessage(ctx, message)
}

func (r *platformRepo) messagesOf(tenantID, conversationID string) []domain.WhatsAppConversationMessage {
	messages, _ := r.ListMessagesByConversation(context.Background(), tenantID, conversationID)
	return messages
}

func (r *platformRepo) ListConversationsByTenantPage(ctx context.Context, tenantID, query, state string, cursor *domain.ConversationCursor, limit int) ([]domain.WhatsAppConversation, error) {
	all, _ := r.ListConversationsByTenant(ctx, tenantID)
	for index := range all {
		all[index].UnreadCount = domain.UnreadInbound(r.messagesOf(tenantID, all[index].ID), all[index].LastReadAt)
	}
	return domain.PageConversations(all, query, state, cursor, limit), nil
}

func (r *platformRepo) ListMessagesPage(_ context.Context, tenantID, conversationID, before string, limit int) ([]domain.WhatsAppConversationMessage, error) {
	return domain.PageMessages(r.messagesOf(tenantID, conversationID), before, limit), nil
}

func (r *platformRepo) MarkConversationRead(_ context.Context, tenantID, conversationID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	conversation, ok := r.conversations[conversationID]
	if ok && conversation.TenantID == tenantID {
		conversation.LastReadAt = &at
		r.conversations[conversationID] = conversation
	}
	return nil
}

func (r *platformRepo) FindMessageByClientID(_ context.Context, tenantID, clientMessageID string) (domain.WhatsAppConversationMessage, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, message := range r.messages {
		if message.TenantID == tenantID && message.ClientMessageID == clientMessageID {
			return message, true, nil
		}
	}
	return domain.WhatsAppConversationMessage{}, false, nil
}

func (r *platformRepo) UpdateMessage(_ context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.messages[message.ID]
	if !ok {
		return domain.WhatsAppConversationMessage{}, errors.New("not found")
	}
	existing.Status, existing.Payload, existing.UpdatedAt = message.Status, message.Payload, message.UpdatedAt
	if message.ProviderMessageID != "" {
		existing.ProviderMessageID = message.ProviderMessageID
		r.providerMessage[message.ProviderMessageID] = existing.ID
	}
	r.messages[existing.ID] = existing
	return existing, nil
}

func (r *platformRepo) ConversationUnreadCount(ctx context.Context, tenantID, conversationID string) (int, error) {
	conversation, ok, _ := r.FindConversationByTenantAndID(ctx, tenantID, conversationID)
	if !ok {
		return 0, nil
	}
	return domain.UnreadInbound(r.messagesOf(tenantID, conversationID), conversation.LastReadAt), nil
}

type recordedEvent struct {
	Name    string
	Created *MessageCreatedView
	Status  *MessageStatusView
	Updated *ConversationUpdatedView
}

type recordingInboxPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (p *recordingInboxPublisher) add(event recordedEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
}

func (p *recordingInboxPublisher) PublishMessageCreated(_ context.Context, payload MessageCreatedView) error {
	p.add(recordedEvent{Name: "message.created", Created: &payload})
	return nil
}

func (p *recordingInboxPublisher) PublishMessageStatus(_ context.Context, payload MessageStatusView) error {
	p.add(recordedEvent{Name: "message.status", Status: &payload})
	return nil
}

func (p *recordingInboxPublisher) PublishConversationUpdated(_ context.Context, payload ConversationUpdatedView) error {
	p.add(recordedEvent{Name: "conversation.updated", Updated: &payload})
	return nil
}

func (p *recordingInboxPublisher) names() string {
	names := make([]string, 0, len(p.events))
	for _, event := range p.events {
		names = append(names, event.Name)
	}
	return strings.Join(names, ",")
}

func (p *recordingInboxPublisher) last(name string) recordedEvent {
	for index := len(p.events) - 1; index >= 0; index-- {
		if p.events[index].Name == name {
			return p.events[index]
		}
	}
	return recordedEvent{}
}

type countingTextProvider struct {
	calls  int
	result domain.WhatsAppSendResult
	err    error
}

func (p *countingTextProvider) SendText(context.Context, string, string, string) (domain.WhatsAppSendResult, error) {
	p.calls++
	return p.result, p.err
}

func (p *countingTextProvider) SendMedia(context.Context, domain.WhatsAppMediaMessageRequest) (domain.WhatsAppSendResult, error) {
	return domain.WhatsAppSendResult{}, errors.New("not used")
}

type sequentialIDs struct {
	mu   sync.Mutex
	next int
}

func (s *sequentialIDs) New(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return fmt.Sprintf("%s-%d", prefix, s.next)
}

type inboxKit struct {
	repo      *platformRepo
	publisher *recordingInboxPublisher
	provider  *countingTextProvider
	outbox    *fakeOutboxRepository
	notifier  *PlatformMessagingNotifier
	send      *PlatformMessagingSendUseCase
	ids       *sequentialIDs
}

func newInboxKit() *inboxKit {
	kit := &inboxKit{
		repo:      &platformRepo{newFakeWhatsAppConversationRepository()},
		publisher: &recordingInboxPublisher{},
		provider:  &countingTextProvider{result: domain.WhatsAppSendResult{Status: "accepted", MessageID: "wamid.out1"}},
		outbox:    newFakeOutboxRepository(),
		ids:       &sequentialIDs{},
	}
	clock := fixedNotificationClock{now: inboxNow}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	kit.notifier = NewPlatformMessagingNotifier(kit.repo, kit.publisher, clock, log)
	text := NewSendWhatsAppTextMessageUseCase(kit.outbox, kit.repo, kit.provider, &fakeDeliveryPublisher{}, clock, kit.ids, identity.SystemTenantID)
	kit.send = NewPlatformMessagingSendUseCase(kit.repo, text, kit.notifier, clock, kit.ids)
	return kit
}

func (k *inboxKit) conversation(t *testing.T, tenantID, id string, windowOpen bool) domain.WhatsAppConversation {
	t.Helper()
	conversation := domain.WhatsAppConversation{
		ID: id, TenantID: tenantID, DriverPhone: "2348000000009", DriverName: "Ada",
		LastMessageAt: &inboxNow, LastMessagePreview: "hi", LastMessageDirection: "inbound",
		CreatedAt: inboxNow, UpdatedAt: inboxNow,
	}
	if windowOpen {
		expires := inboxNow.Add(2 * time.Hour)
		conversation.WindowExpiresAt = &expires
	}
	saved, err := k.repo.SaveConversation(context.Background(), conversation)
	if err != nil {
		t.Fatalf("save conversation: %v", err)
	}
	return saved
}
