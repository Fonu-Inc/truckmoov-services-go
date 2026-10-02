package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

var (
	_ application.PlatformMessagingRepository  = (*PostgresWhatsAppConversationRepository)(nil)
	_ application.PlatformMessagingRepository  = (*InMemoryWhatsAppConversationRepository)(nil)
	_ application.PlatformInboxAdminRepository = (*PostgresWhatsAppConversationRepository)(nil)
	_ application.PlatformInboxAdminRepository = (*InMemoryWhatsAppConversationRepository)(nil)
)

const unreadCountSQL = `GREATEST((SELECT count(*) FROM whatsapp_conversation_messages m
    WHERE m.tenant_id = c.tenant_id AND m.conversation_id = c.id AND m.direction = 'inbound'
      AND (c.last_read_at IS NULL OR m.created_at > c.last_read_at)), CASE WHEN c.marked_unread THEN 1 ELSE 0 END)`

const conversationColumnsSQL = `id, tenant_id, driver_phone, driver_name, last_inbound_message_at, last_outbound_message_at, last_template_sent_at, window_expires_at, last_message_at, last_message_preview, last_message_direction, last_message_type, last_read_at, created_at, updated_at, status, priority, assignee_user_id, tags, marked_unread`

const messageColumnsSQL = `id, tenant_id, conversation_id, provider_message_id, client_message_id, direction, message_type, status, sender_role, sender_subject, from_phone, to_phone, text_content, template_name, media_type, media_id, media_link, caption, raw_payload, created_at, updated_at`

const conversationPageSQL = `
SELECT ` + conversationColumnsSQL + `, unread_count
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
  AND ($7::text = '' OR status = $7::text)
  AND ($8::text = '' OR ($8::text = 'unassigned' AND assignee_user_id = '') OR assignee_user_id = $8::text)
  AND ($9::text = '' OR tags @> jsonb_build_array($9::text))
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
	return r.ListConversationsFiltered(ctx, tenantID, domain.InboxFilter{Query: query, State: state}, cursor, limit)
}

func (r *PostgresWhatsAppConversationRepository) ListConversationsFiltered(ctx context.Context, tenantID string, filter domain.InboxFilter, cursor *domain.ConversationCursor, limit int) ([]domain.WhatsAppConversation, error) {
	var cursorAt sql.NullTime
	cursorID := ""
	if cursor != nil {
		cursorAt = sql.NullTime{Time: cursor.SortAt.UTC(), Valid: true}
		cursorID = cursor.ID
	}
	rows, err := r.db.QueryContext(ctx, conversationPageSQL, strings.TrimSpace(tenantID), strings.TrimSpace(filter.Query), inboxState(filter.State), cursorAt, cursorID, limit, filter.Status, filter.Assignee, filter.Tag)
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
SELECT `+messageColumnsSQL+`
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
UPDATE whatsapp_conversations SET last_read_at = GREATEST($3, last_read_at), marked_unread = FALSE
WHERE tenant_id = $1 AND id = $2 AND (last_read_at IS NULL OR last_read_at < $3 OR marked_unread)
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
SET status = CASE WHEN status IN ('delivered', 'read') AND $3 IN ('queued', 'sent') THEN status ELSE $3 END,
    provider_message_id = COALESCE($4, provider_message_id), raw_payload = $5, updated_at = $6
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

func advancedStoredStatus(current, next string) string {
	if (current == "delivered" || current == "read") && (next == "queued" || next == "sent") {
		return current
	}
	return next
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
		conversation.UnreadCount = domain.EffectiveUnread(domain.UnreadInbound(r.messagesOf(conversation.TenantID, conversation.ID), conversation.LastReadAt), conversation.MarkedUnread)
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

func (r *InMemoryWhatsAppConversationRepository) ListConversationsByTenantPage(ctx context.Context, tenantID, query, state string, cursor *domain.ConversationCursor, limit int) ([]domain.WhatsAppConversation, error) {
	return r.ListConversationsFiltered(ctx, tenantID, domain.InboxFilter{Query: query, State: state}, cursor, limit)
}

func (r *InMemoryWhatsAppConversationRepository) ListConversationsFiltered(_ context.Context, tenantID string, filter domain.InboxFilter, cursor *domain.ConversationCursor, limit int) ([]domain.WhatsAppConversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	filter.State = inboxState(filter.State)
	return domain.PageConversationsFiltered(r.tenantConversationsWithUnread(tenantID), filter, cursor, limit), nil
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
	}
	conversation.MarkedUnread = false
	r.conversations[conversation.ID] = conversation
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
	existing.Status = advancedStoredStatus(existing.Status, message.Status)
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
	return domain.EffectiveUnread(domain.UnreadInbound(r.messagesOf(conversation.TenantID, conversation.ID), conversation.LastReadAt), conversation.MarkedUnread), nil
}

func (r *PostgresWhatsAppConversationRepository) PatchConversation(ctx context.Context, tenantID, id string, patch domain.ConversationPatch, at time.Time) (domain.WhatsAppConversation, bool, error) {
	var tags any
	if patch.Tags != nil {
		encoded, err := json.Marshal(*patch.Tags)
		if err != nil {
			return domain.WhatsAppConversation{}, false, fmt.Errorf("marshal conversation tags: %w", err)
		}
		tags = string(encoded)
	}
	row := r.db.QueryRowContext(ctx, `
UPDATE whatsapp_conversations SET
    driver_name = COALESCE($3::text, driver_name),
    status = COALESCE($4::text, status),
    priority = COALESCE($5::text, priority),
    assignee_user_id = COALESCE($6::text, assignee_user_id),
    tags = COALESCE($7::jsonb, tags),
    marked_unread = COALESCE($8::boolean, marked_unread),
    updated_at = $9
WHERE tenant_id = $1 AND id = $2
RETURNING `+conversationColumnsSQL, strings.TrimSpace(tenantID), strings.TrimSpace(id),
		patch.Name, patch.Status, patch.Priority, patch.AssigneeUserID, tags, patch.MarkedUnread, at.UTC())
	conversation, err := scanConversation(row)
	if err == sql.ErrNoRows {
		return domain.WhatsAppConversation{}, false, nil
	}
	return conversation, err == nil, err
}

func (r *PostgresWhatsAppConversationRepository) FindMessageByID(ctx context.Context, tenantID, id string) (domain.WhatsAppConversationMessage, bool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+messageColumnsSQL+` FROM whatsapp_conversation_messages WHERE tenant_id = $1 AND id = $2`,
		strings.TrimSpace(tenantID), strings.TrimSpace(id))
	return scanOptionalMessage(row)
}

func (r *PostgresWhatsAppConversationRepository) DeleteScheduledMessage(ctx context.Context, tenantID, id string) (domain.WhatsAppConversationMessage, bool, error) {
	row := r.db.QueryRowContext(ctx, `
DELETE FROM whatsapp_conversation_messages
WHERE tenant_id = $1 AND id = $2 AND status = 'scheduled'
RETURNING `+messageColumnsSQL, strings.TrimSpace(tenantID), strings.TrimSpace(id))
	return scanOptionalMessage(row)
}

func (r *PostgresWhatsAppConversationRepository) ClaimScheduledMessage(ctx context.Context, tenantID, id string, now time.Time) (domain.WhatsAppConversationMessage, bool, error) {
	row := r.db.QueryRowContext(ctx, `
UPDATE whatsapp_conversation_messages SET status = 'queued', created_at = $3, updated_at = $3
WHERE tenant_id = $1 AND id = $2 AND status = 'scheduled'
RETURNING `+messageColumnsSQL, strings.TrimSpace(tenantID), strings.TrimSpace(id), now.UTC())
	return scanOptionalMessage(row)
}

func (r *PostgresWhatsAppConversationRepository) ListDueScheduledMessages(ctx context.Context, tenantID string, now time.Time, limit int) ([]domain.WhatsAppConversationMessage, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT `+messageColumnsSQL+`
FROM whatsapp_conversation_messages
WHERE tenant_id = $1 AND status = 'scheduled' AND (raw_payload->>'scheduled_at')::timestamptz <= $2
ORDER BY (raw_payload->>'scheduled_at')::timestamptz ASC
LIMIT $3
`, strings.TrimSpace(tenantID), now.UTC(), limit)
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

func scanOptionalMessage(row sqlScanner) (domain.WhatsAppConversationMessage, bool, error) {
	message, err := scanConversationMessage(row)
	if err == sql.ErrNoRows {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	return message, err == nil, err
}

func (r *InMemoryWhatsAppConversationRepository) PatchConversation(_ context.Context, tenantID, id string, patch domain.ConversationPatch, at time.Time) (domain.WhatsAppConversation, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	conversation, ok := r.conversations[strings.TrimSpace(id)]
	if !ok || conversation.TenantID != strings.TrimSpace(tenantID) {
		return domain.WhatsAppConversation{}, false, nil
	}
	conversation = domain.ApplyConversationPatch(conversation, patch)
	conversation.UpdatedAt = at.UTC()
	r.conversations[conversation.ID] = cloneConversation(conversation)
	return cloneConversation(conversation), true, nil
}

func (r *InMemoryWhatsAppConversationRepository) FindMessageByID(_ context.Context, tenantID, id string) (domain.WhatsAppConversationMessage, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	message, ok := r.messages[strings.TrimSpace(id)]
	if !ok || message.TenantID != strings.TrimSpace(tenantID) {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	return cloneConversationMessage(message), true, nil
}

func (r *InMemoryWhatsAppConversationRepository) DeleteScheduledMessage(_ context.Context, tenantID, id string) (domain.WhatsAppConversationMessage, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	message, ok := r.messages[strings.TrimSpace(id)]
	if !ok || message.TenantID != strings.TrimSpace(tenantID) || message.Status != "scheduled" {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	delete(r.messages, message.ID)
	delete(r.clientMessages, clientMessageKey(message.TenantID, message.ClientMessageID))
	return cloneConversationMessage(message), true, nil
}

func (r *InMemoryWhatsAppConversationRepository) ClaimScheduledMessage(_ context.Context, tenantID, id string, now time.Time) (domain.WhatsAppConversationMessage, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	message, ok := r.messages[strings.TrimSpace(id)]
	if !ok || message.TenantID != strings.TrimSpace(tenantID) || message.Status != "scheduled" {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	message.Status = "queued"
	message.CreatedAt = now.UTC()
	message.UpdatedAt = now.UTC()
	r.messages[message.ID] = cloneConversationMessage(message)
	return cloneConversationMessage(message), true, nil
}

func (r *InMemoryWhatsAppConversationRepository) ListDueScheduledMessages(_ context.Context, tenantID string, now time.Time, limit int) ([]domain.WhatsAppConversationMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	due := make([]domain.WhatsAppConversationMessage, 0)
	for _, message := range r.messages {
		if message.TenantID != strings.TrimSpace(tenantID) || message.Status != "scheduled" {
			continue
		}
		if at, ok := domain.ScheduledAt(message); ok && !at.After(now) {
			due = append(due, cloneConversationMessage(message))
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].CreatedAt.Before(due[j].CreatedAt) })
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}
