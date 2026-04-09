package application

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeWhatsAppConversationRepository struct {
	mu              sync.RWMutex
	conversations   map[string]domain.WhatsAppConversation
	byTenantPhone   map[string]string
	messages        map[string]domain.WhatsAppConversationMessage
	providerMessage map[string]string
}

func newFakeWhatsAppConversationRepository() *fakeWhatsAppConversationRepository {
	return &fakeWhatsAppConversationRepository{
		conversations:   map[string]domain.WhatsAppConversation{},
		byTenantPhone:   map[string]string{},
		messages:        map[string]domain.WhatsAppConversationMessage{},
		providerMessage: map[string]string{},
	}
}

func (r *fakeWhatsAppConversationRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakeWhatsAppConversationRepository) FindConversationByTenantAndID(_ context.Context, tenantID, id string) (domain.WhatsAppConversation, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conversation, ok := r.conversations[strings.TrimSpace(id)]
	if !ok || conversation.TenantID != strings.TrimSpace(tenantID) {
		return domain.WhatsAppConversation{}, false, nil
	}
	return cloneTestConversation(conversation), true, nil
}

func (r *fakeWhatsAppConversationRepository) FindConversationByTenantAndDriverPhone(_ context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byTenantPhone[testTenantPhoneKey(tenantID, driverPhone)]
	if !ok {
		return domain.WhatsAppConversation{}, false, nil
	}
	conversation := r.conversations[id]
	return cloneTestConversation(conversation), true, nil
}

func (r *fakeWhatsAppConversationRepository) FindMostRecentConversationByDriverPhone(_ context.Context, driverPhone string) (domain.WhatsAppConversation, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest domain.WhatsAppConversation
	found := false
	for _, conversation := range r.conversations {
		if conversation.DriverPhone != strings.TrimSpace(driverPhone) {
			continue
		}
		if !found || conversation.UpdatedAt.After(latest.UpdatedAt) {
			latest = conversation
			found = true
		}
	}
	if !found {
		return domain.WhatsAppConversation{}, false, nil
	}
	return cloneTestConversation(latest), true, nil
}

func (r *fakeWhatsAppConversationRepository) FindMostRecentConversationByTenantAndDriverPhone(_ context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest domain.WhatsAppConversation
	found := false
	for _, conversation := range r.conversations {
		if conversation.TenantID != strings.TrimSpace(tenantID) || conversation.DriverPhone != strings.TrimSpace(driverPhone) {
			continue
		}
		if !found || conversation.UpdatedAt.After(latest.UpdatedAt) {
			latest = conversation
			found = true
		}
	}
	if !found {
		return domain.WhatsAppConversation{}, false, nil
	}
	return cloneTestConversation(latest), true, nil
}

func (r *fakeWhatsAppConversationRepository) ListConversationsByTenant(_ context.Context, tenantID string) ([]domain.WhatsAppConversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conversations := make([]domain.WhatsAppConversation, 0)
	for _, conversation := range r.conversations {
		if conversation.TenantID == strings.TrimSpace(tenantID) {
			conversations = append(conversations, cloneTestConversation(conversation))
		}
	}
	sort.Slice(conversations, func(i, j int) bool {
		left := conversations[i].UpdatedAt
		right := conversations[j].UpdatedAt
		if conversations[i].LastMessageAt != nil {
			left = conversations[i].LastMessageAt.UTC()
		}
		if conversations[j].LastMessageAt != nil {
			right = conversations[j].LastMessageAt.UTC()
		}
		return left.After(right)
	})
	return conversations, nil
}

func (r *fakeWhatsAppConversationRepository) SaveConversation(_ context.Context, conversation domain.WhatsAppConversation) (domain.WhatsAppConversation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := testTenantPhoneKey(conversation.TenantID, conversation.DriverPhone)
	if existingID, ok := r.byTenantPhone[key]; ok {
		existing := r.conversations[existingID]
		conversation.ID = existing.ID
		if strings.TrimSpace(conversation.DriverName) == "" {
			conversation.DriverName = existing.DriverName
		}
		if conversation.LastInboundMessageAt == nil {
			conversation.LastInboundMessageAt = cloneTestTime(existing.LastInboundMessageAt)
		}
		if conversation.LastOutboundMessageAt == nil {
			conversation.LastOutboundMessageAt = cloneTestTime(existing.LastOutboundMessageAt)
		}
		if conversation.LastTemplateSentAt == nil {
			conversation.LastTemplateSentAt = cloneTestTime(existing.LastTemplateSentAt)
		}
		if conversation.WindowExpiresAt == nil {
			conversation.WindowExpiresAt = cloneTestTime(existing.WindowExpiresAt)
		}
		if conversation.LastMessageAt == nil {
			conversation.LastMessageAt = cloneTestTime(existing.LastMessageAt)
		}
		if strings.TrimSpace(conversation.LastMessagePreview) == "" {
			conversation.LastMessagePreview = existing.LastMessagePreview
		}
		if strings.TrimSpace(conversation.LastMessageDirection) == "" {
			conversation.LastMessageDirection = existing.LastMessageDirection
		}
		if strings.TrimSpace(conversation.LastMessageType) == "" {
			conversation.LastMessageType = existing.LastMessageType
		}
		if conversation.CreatedAt.IsZero() {
			conversation.CreatedAt = existing.CreatedAt
		}
	}
	if conversation.CreatedAt.IsZero() {
		conversation.CreatedAt = time.Now().UTC()
	}
	if conversation.UpdatedAt.IsZero() {
		conversation.UpdatedAt = conversation.CreatedAt
	}
	r.conversations[conversation.ID] = cloneTestConversation(conversation)
	r.byTenantPhone[key] = conversation.ID
	return cloneTestConversation(conversation), nil
}

func (r *fakeWhatsAppConversationRepository) SaveMessage(_ context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(message.ProviderMessageID) != "" {
		if existingID, ok := r.providerMessage[strings.TrimSpace(message.ProviderMessageID)]; ok {
			existing := r.messages[existingID]
			message.ID = existing.ID
		}
	}
	r.messages[message.ID] = cloneTestConversationMessage(message)
	if strings.TrimSpace(message.ProviderMessageID) != "" {
		r.providerMessage[strings.TrimSpace(message.ProviderMessageID)] = message.ID
	}
	return cloneTestConversationMessage(message), nil
}

func (r *fakeWhatsAppConversationRepository) ListMessagesByConversation(_ context.Context, tenantID, conversationID string) ([]domain.WhatsAppConversationMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	messages := make([]domain.WhatsAppConversationMessage, 0)
	for _, message := range r.messages {
		if message.TenantID == strings.TrimSpace(tenantID) && message.ConversationID == strings.TrimSpace(conversationID) {
			messages = append(messages, cloneTestConversationMessage(message))
		}
	}
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return messages, nil
}

func (r *fakeWhatsAppConversationRepository) FindMessageByProviderMessageID(_ context.Context, providerMessageID string) (domain.WhatsAppConversationMessage, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.providerMessage[strings.TrimSpace(providerMessageID)]
	if !ok {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	message := r.messages[id]
	return cloneTestConversationMessage(message), true, nil
}

func testTenantPhoneKey(tenantID, driverPhone string) string {
	return strings.TrimSpace(tenantID) + "::" + strings.TrimSpace(driverPhone)
}

func cloneTestConversation(conversation domain.WhatsAppConversation) domain.WhatsAppConversation {
	conversation.LastInboundMessageAt = cloneTestTime(conversation.LastInboundMessageAt)
	conversation.LastOutboundMessageAt = cloneTestTime(conversation.LastOutboundMessageAt)
	conversation.LastTemplateSentAt = cloneTestTime(conversation.LastTemplateSentAt)
	conversation.WindowExpiresAt = cloneTestTime(conversation.WindowExpiresAt)
	conversation.LastMessageAt = cloneTestTime(conversation.LastMessageAt)
	return conversation
}

func cloneTestConversationMessage(message domain.WhatsAppConversationMessage) domain.WhatsAppConversationMessage {
	if message.Payload != nil {
		cloned := make(map[string]any, len(message.Payload))
		for key, value := range message.Payload {
			cloned[key] = value
		}
		message.Payload = cloned
	}
	return message
}

func cloneTestTime(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
