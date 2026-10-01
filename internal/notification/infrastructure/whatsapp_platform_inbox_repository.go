package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

var (
	_ application.PlatformMessagingRepository = (*PostgresWhatsAppConversationRepository)(nil)
	_ application.PlatformMessagingRepository = (*InMemoryWhatsAppConversationRepository)(nil)
)

const unreadCountSQL = `(SELECT count(*) FROM whatsapp_conversation_messages m
    WHERE m.tenant_id = c.tenant_id AND m.conversation_id = c.id AND m.direction = 'inbound'
      AND (c.last_read_at IS NULL OR m.created_at > c.last_read_at))`

const conversationPageSQL = `
SELECT id, tenant_id, driver_phone, driver_name, last_inbound_message_at, last_outbound_message_at, last_template_sent_at, window_expires_at, last_message_at, last_message_preview, last_message_direction, last_message_type, last_read_at, created_at, updated_at, unread_count
FROM (
    SELECT c.*, COALESCE(c.last_message_at, c.created_at) AS sort_at, ` + unreadCountSQL + ` AS unread_count
    FROM whatsapp_conversations c
    WHERE c.tenant_id = $1
      AND ($2::text = ''
           OR position(lower($2::text) in lower(c.driver_phone)) > 0
           OR position(lower($2::text) in lower(c.driver_name)) > 0
           OR position(lower($2::text) in lower(c.last_message_preview)) > 0)
) t
WHERE ($3::text = 'all' OR unread_count > 0)
  AND ($4::timestamptz IS NULL OR (sort_at, id) < ($4::timestamptz, $5::text))
ORDER BY sort_at DESC, id DESC
LIMIT $6
`

type unreadScanner struct {
	sqlScanner
	unread *int
}

func (s unreadScanner) Scan(dest ...any) error {
	return s.sqlScanner.Scan(append(dest, s.unread)...)
}

func (r *PostgresWhatsAppConversationRepository) ListConversationsByTenantPage(ctx context.Context, tenantID, query, state string, cursor *domain.ConversationCursor, limit int) ([]domain.WhatsAppConversation, error) {
	var cursorAt sql.NullTime
	cursorID := ""
	if cursor != nil {
		cursorAt = sql.NullTime{Time: cursor.SortAt.UTC(), Valid: true}
		cursorID = cursor.ID
	}
	rows, err := r.db.QueryContext(ctx, conversationPageSQL, strings.TrimSpace(tenantID), strings.TrimSpace(query), inboxState(state), cursorAt, cursorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	conversations := make([]domain.WhatsAppConversation, 0, limit)
	for rows.Next() {
		var unread int
		conversation, err := scanConversation(unreadScanner{sqlScanner: rows, unread: &unread})
		if err != nil {
			return nil, err
		}
		conversation.UnreadCount = unread
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

func inboxState(state string) string {
	if state == domain.InboxStateUnread {
		return domain.InboxStateUnread
	}
	return domain.InboxStateAll
}

func (r *PostgresWhatsAppConversationRepository) ListMessagesPage(ctx context.Context, tenantID, conversationID, before string, limit int) ([]domain.WhatsAppConversationMessage, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, tenant_id, conversation_id, provider_message_id, client_message_id, direction, message_type, status, sender_role, sender_subject, from_phone, to_phone, text_content, template_name, media_type, media_id, media_link, caption, raw_payload, created_at, updated_at
FROM whatsapp_conversation_messages
WHERE tenant_id = $1 AND conversation_id = $2
  AND ($3::text = '' OR (created_at, id) < (
        SELECT created_at, id FROM whatsapp_conversation_messages
        WHERE tenant_id = $1 AND conversation_id = $2 AND id = $3::text))
ORDER BY created_at DESC, id DESC
LIMIT $4
`, strings.TrimSpace(tenantID), strings.TrimSpace(conversationID), strings.TrimSpace(before), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]domain.WhatsAppConversationMessage, 0, limit)
	for rows.Next() {
		message, err := scanConversationMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (r *PostgresWhatsAppConversationRepository) MarkConversationRead(ctx context.Context, tenantID, conversationID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE whatsapp_conversations SET last_read_at = $3
WHERE tenant_id = $1 AND id = $2 AND (last_read_at IS NULL OR last_read_at < $3)
`, strings.TrimSpace(tenantID), strings.TrimSpace(conversationID), at.UTC())
	return err
}

func (r *PostgresWhatsAppConversationRepository) FindMessageByClientID(ctx context.Context, tenantID, clientMessageID string) (domain.WhatsAppConversationMessage, bool, error) {
	trimmed := strings.TrimSpace(clientMessageID)
	if trimmed == "" {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	row := r.db.QueryRowContext(ctx, `
SELECT id, tenant_id, conversation_id, provider_message_id, client_message_id, direction, message_type, status, sender_role, sender_subject, from_phone, to_phone, text_content, template_name, media_type, media_id, media_link, caption, raw_payload, created_at, updated_at
FROM whatsapp_conversation_messages
WHERE tenant_id = $1 AND client_message_id = $2
`, strings.TrimSpace(tenantID), trimmed)
	message, err := scanConversationMessage(row)
	if err == sql.ErrNoRows {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	if err != nil {
		return domain.WhatsAppConversationMessage{}, false, err
	}
	return message, true, nil
}

func (r *PostgresWhatsAppConversationRepository) UpdateMessage(ctx context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error) {
	payload, err := json.Marshal(message.Payload)
	if err != nil {
		return domain.WhatsAppConversationMessage{}, fmt.Errorf("marshal whatsapp conversation message payload: %w", err)
	}
	row := r.db.QueryRowContext(ctx, `
UPDATE whatsapp_conversation_messages
SET status = $3, provider_message_id = COALESCE($4, provider_message_id), raw_payload = $5, updated_at = $6
WHERE tenant_id = $1 AND id = $2
RETURNING id, tenant_id, conversation_id, provider_message_id, client_message_id, direction, message_type, status, sender_role, sender_subject, from_phone, to_phone, text_content, template_name, media_type, media_id, media_link, caption, raw_payload, created_at, updated_at
`, strings.TrimSpace(message.TenantID), strings.TrimSpace(message.ID), strings.TrimSpace(message.Status), nullableString(message.ProviderMessageID), payload, message.UpdatedAt.UTC())
	return scanConversationMessage(row)
}

func (r *PostgresWhatsAppConversationRepository) ConversationUnreadCount(ctx context.Context, tenantID, conversationID string) (int, error) {
	var unread int
	err := r.db.QueryRowContext(ctx, `
SELECT `+unreadCountSQL+`
FROM whatsapp_conversations c
WHERE c.tenant_id = $1 AND c.id = $2
`, strings.TrimSpace(tenantID), strings.TrimSpace(conversationID)).Scan(&unread)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return unread, err
}

func clientMessageKey(tenantID, clientMessageID string) string {
	return strings.TrimSpace(tenantID) + "::" + strings.TrimSpace(clientMessageID)
}

func (r *InMemoryWhatsAppConversationRepository) tenantConversationsWithUnread(tenantID string) []domain.WhatsAppConversation {
	conversations := make([]domain.WhatsAppConversation, 0)
	for _, conversation := range r.conversations {
		if conversation.TenantID != strings.TrimSpace(tenantID) {
			continue
		}
		conversation = cloneConversation(conversation)
		conversation.UnreadCount = domain.UnreadInbound(r.messagesOf(conversation.TenantID, conversation.ID), conversation.LastReadAt)
		conversations = append(conversations, conversation)
	}
	return conversations
}

func (r *InMemoryWhatsAppConversationRepository) messagesOf(tenantID, conversationID string) []domain.WhatsAppConversationMessage {
	messages := make([]domain.WhatsAppConversationMessage, 0)
	for _, message := range r.messages {
		if message.TenantID == tenantID && message.ConversationID == conversationID {
			messages = append(messages, cloneConversationMessage(message))
		}
	}
	return messages
}

func (r *InMemoryWhatsAppConversationRepository) ListConversationsByTenantPage(_ context.Context, tenantID, query, state string, cursor *domain.ConversationCursor, limit int) ([]domain.WhatsAppConversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return domain.PageConversations(r.tenantConversationsWithUnread(tenantID), query, inboxState(state), cursor, limit), nil
}

func (r *InMemoryWhatsAppConversationRepository) ListMessagesPage(_ context.Context, tenantID, conversationID, before string, limit int) ([]domain.WhatsAppConversationMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return domain.PageMessages(r.messagesOf(strings.TrimSpace(tenantID), strings.TrimSpace(conversationID)), strings.TrimSpace(before), limit), nil
}

func (r *InMemoryWhatsAppConversationRepository) MarkConversationRead(_ context.Context, tenantID, conversationID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	conversation, ok := r.conversations[strings.TrimSpace(conversationID)]
	if !ok || conversation.TenantID != strings.TrimSpace(tenantID) {
		return nil
	}
	if conversation.LastReadAt == nil || conversation.LastReadAt.Before(at) {
		conversation.LastReadAt = cloneTime(&at)
		r.conversations[conversation.ID] = conversation
	}
	return nil
}

func (r *InMemoryWhatsAppConversationRepository) FindMessageByClientID(_ context.Context, tenantID, clientMessageID string) (domain.WhatsAppConversationMessage, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.clientMessages[clientMessageKey(tenantID, clientMessageID)]
	if !ok {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	return cloneConversationMessage(r.messages[id]), true, nil
}

func (r *InMemoryWhatsAppConversationRepository) UpdateMessage(_ context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.messages[strings.TrimSpace(message.ID)]
	if !ok || existing.TenantID != strings.TrimSpace(message.TenantID) {
		return domain.WhatsAppConversationMessage{}, sql.ErrNoRows
	}
	existing.Status = message.Status
	existing.Payload = message.Payload
	existing.UpdatedAt = message.UpdatedAt
	if provider := strings.TrimSpace(message.ProviderMessageID); provider != "" {
		existing.ProviderMessageID = provider
		r.providerMessages[provider] = existing.ID
	}
	r.messages[existing.ID] = cloneConversationMessage(existing)
	return cloneConversationMessage(existing), nil
}

func (r *InMemoryWhatsAppConversationRepository) ConversationUnreadCount(_ context.Context, tenantID, conversationID string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conversation, ok := r.conversations[strings.TrimSpace(conversationID)]
	if !ok || conversation.TenantID != strings.TrimSpace(tenantID) {
		return 0, nil
	}
	return domain.UnreadInbound(r.messagesOf(conversation.TenantID, conversation.ID), conversation.LastReadAt), nil
}
