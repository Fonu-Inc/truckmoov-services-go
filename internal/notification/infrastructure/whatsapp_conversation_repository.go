package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

var errDuplicateClientMessageID = errors.New("client message id already used")

const whatsappConversationSchema = `
CREATE TABLE IF NOT EXISTS whatsapp_conversations (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    driver_phone TEXT NOT NULL,
    driver_name TEXT NOT NULL DEFAULT '',
    last_inbound_message_at TIMESTAMPTZ NULL,
    last_outbound_message_at TIMESTAMPTZ NULL,
    last_template_sent_at TIMESTAMPTZ NULL,
    window_expires_at TIMESTAMPTZ NULL,
    last_message_at TIMESTAMPTZ NULL,
    last_message_preview TEXT NOT NULL DEFAULT '',
    last_message_direction TEXT NOT NULL DEFAULT '',
    last_message_type TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, driver_phone)
);

CREATE INDEX IF NOT EXISTS idx_whatsapp_conversations_tenant_last_message_at
    ON whatsapp_conversations (tenant_id, COALESCE(last_message_at, created_at) DESC);

CREATE INDEX IF NOT EXISTS idx_whatsapp_conversations_driver_phone
    ON whatsapp_conversations (driver_phone, updated_at DESC);

CREATE TABLE IF NOT EXISTS whatsapp_conversation_messages (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL REFERENCES whatsapp_conversations(id) ON DELETE CASCADE,
    provider_message_id TEXT NULL UNIQUE,
    direction TEXT NOT NULL,
    message_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT '',
    sender_role TEXT NOT NULL DEFAULT '',
    sender_subject TEXT NOT NULL DEFAULT '',
    from_phone TEXT NOT NULL DEFAULT '',
    to_phone TEXT NOT NULL DEFAULT '',
    text_content TEXT NOT NULL DEFAULT '',
    template_name TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    media_id TEXT NOT NULL DEFAULT '',
    media_link TEXT NOT NULL DEFAULT '',
    caption TEXT NOT NULL DEFAULT '',
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_whatsapp_conversation_messages_tenant_conversation_created_at
    ON whatsapp_conversation_messages (tenant_id, conversation_id, created_at ASC);

ALTER TABLE whatsapp_conversations ADD COLUMN IF NOT EXISTS last_read_at TIMESTAMPTZ NULL;
ALTER TABLE whatsapp_conversation_messages ADD COLUMN IF NOT EXISTS client_message_id TEXT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_whatsapp_conversation_messages_tenant_client_message_id
    ON whatsapp_conversation_messages (tenant_id, client_message_id)
    WHERE client_message_id IS NOT NULL;

ALTER TABLE whatsapp_conversations ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'open';
ALTER TABLE whatsapp_conversations ADD COLUMN IF NOT EXISTS priority TEXT NOT NULL DEFAULT 'normal';
ALTER TABLE whatsapp_conversations ADD COLUMN IF NOT EXISTS assignee_user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_conversations ADD COLUMN IF NOT EXISTS tags JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE whatsapp_conversations ADD COLUMN IF NOT EXISTS marked_unread BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_whatsapp_conversations_tenant_assignee_status
    ON whatsapp_conversations (tenant_id, assignee_user_id, status);

CREATE TABLE IF NOT EXISTS platform_canned_replies (
    id TEXT PRIMARY KEY,
    shortcut TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
`

type PostgresWhatsAppConversationRepository struct {
	db *sql.DB
}

func NewPostgresWhatsAppConversationRepository(db *sql.DB) *PostgresWhatsAppConversationRepository {
	return &PostgresWhatsAppConversationRepository{db: db}
}

func (r *PostgresWhatsAppConversationRepository) EnsureSchema(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, whatsappConversationSchema); err != nil {
		return fmt.Errorf("ensure whatsapp conversation schema: %w", err)
	}
	return nil
}

func (r *PostgresWhatsAppConversationRepository) SaveConversation(ctx context.Context, conversation domain.WhatsAppConversation) (domain.WhatsAppConversation, error) {
	row := r.db.QueryRowContext(ctx, `
INSERT INTO whatsapp_conversations (
    id,
    tenant_id,
    driver_phone,
    driver_name,
    last_inbound_message_at,
    last_outbound_message_at,
    last_template_sent_at,
    window_expires_at,
    last_message_at,
    last_message_preview,
    last_message_direction,
    last_message_type,
    created_at,
    updated_at
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
)
ON CONFLICT (tenant_id, driver_phone) DO UPDATE SET
    driver_name = CASE WHEN EXCLUDED.driver_name <> '' THEN EXCLUDED.driver_name ELSE whatsapp_conversations.driver_name END,
    last_inbound_message_at = GREATEST(EXCLUDED.last_inbound_message_at, whatsapp_conversations.last_inbound_message_at),
    last_outbound_message_at = GREATEST(EXCLUDED.last_outbound_message_at, whatsapp_conversations.last_outbound_message_at),
    last_template_sent_at = GREATEST(EXCLUDED.last_template_sent_at, whatsapp_conversations.last_template_sent_at),
    window_expires_at = GREATEST(EXCLUDED.window_expires_at, whatsapp_conversations.window_expires_at),
    last_message_preview = CASE WHEN EXCLUDED.last_message_preview <> '' AND (whatsapp_conversations.last_message_at IS NULL OR EXCLUDED.last_message_at >= whatsapp_conversations.last_message_at) THEN EXCLUDED.last_message_preview ELSE whatsapp_conversations.last_message_preview END,
    last_message_direction = CASE WHEN EXCLUDED.last_message_direction <> '' AND (whatsapp_conversations.last_message_at IS NULL OR EXCLUDED.last_message_at >= whatsapp_conversations.last_message_at) THEN EXCLUDED.last_message_direction ELSE whatsapp_conversations.last_message_direction END,
    last_message_type = CASE WHEN EXCLUDED.last_message_type <> '' AND (whatsapp_conversations.last_message_at IS NULL OR EXCLUDED.last_message_at >= whatsapp_conversations.last_message_at) THEN EXCLUDED.last_message_type ELSE whatsapp_conversations.last_message_type END,
    last_message_at = GREATEST(EXCLUDED.last_message_at, whatsapp_conversations.last_message_at),
    status = CASE WHEN whatsapp_conversations.status = 'resolved' AND EXCLUDED.last_inbound_message_at > COALESCE(whatsapp_conversations.last_inbound_message_at, 'epoch'::timestamptz) THEN 'open' ELSE whatsapp_conversations.status END,
    updated_at = EXCLUDED.updated_at
RETURNING
    id,
    tenant_id,
    driver_phone,
    driver_name,
    last_inbound_message_at,
    last_outbound_message_at,
    last_template_sent_at,
    window_expires_at,
    last_message_at,
    last_message_preview,
    last_message_direction,
    last_message_type,
    last_read_at,
    created_at,
    updated_at,
    status,
    priority,
    assignee_user_id,
    tags,
    marked_unread
`,
		strings.TrimSpace(conversation.ID),
		strings.TrimSpace(conversation.TenantID),
		strings.TrimSpace(conversation.DriverPhone),
		strings.TrimSpace(conversation.DriverName),
		nullableTime(conversation.LastInboundMessageAt),
		nullableTime(conversation.LastOutboundMessageAt),
		nullableTime(conversation.LastTemplateSentAt),
		nullableTime(conversation.WindowExpiresAt),
		nullableTime(conversation.LastMessageAt),
		strings.TrimSpace(conversation.LastMessagePreview),
		strings.TrimSpace(conversation.LastMessageDirection),
		strings.TrimSpace(conversation.LastMessageType),
		conversation.CreatedAt.UTC(),
		conversation.UpdatedAt.UTC(),
	)
	return scanConversation(row)
}

func (r *PostgresWhatsAppConversationRepository) FindConversationByTenantAndID(ctx context.Context, tenantID, id string) (domain.WhatsAppConversation, bool, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, tenant_id, driver_phone, driver_name, last_inbound_message_at, last_outbound_message_at, last_template_sent_at, window_expires_at, last_message_at, last_message_preview, last_message_direction, last_message_type, last_read_at, created_at, updated_at, status, priority, assignee_user_id, tags, marked_unread
FROM whatsapp_conversations
WHERE tenant_id = $1 AND id = $2
`, strings.TrimSpace(tenantID), strings.TrimSpace(id))
	conversation, err := scanConversation(row)
	if err == sql.ErrNoRows {
		return domain.WhatsAppConversation{}, false, nil
	}
	if err != nil {
		return domain.WhatsAppConversation{}, false, err
	}
	return conversation, true, nil
}

func (r *PostgresWhatsAppConversationRepository) FindConversationByTenantAndDriverPhone(ctx context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, tenant_id, driver_phone, driver_name, last_inbound_message_at, last_outbound_message_at, last_template_sent_at, window_expires_at, last_message_at, last_message_preview, last_message_direction, last_message_type, last_read_at, created_at, updated_at, status, priority, assignee_user_id, tags, marked_unread
FROM whatsapp_conversations
WHERE tenant_id = $1 AND driver_phone = $2
`, strings.TrimSpace(tenantID), strings.TrimSpace(driverPhone))
	conversation, err := scanConversation(row)
	if err == sql.ErrNoRows {
		return domain.WhatsAppConversation{}, false, nil
	}
	if err != nil {
		return domain.WhatsAppConversation{}, false, err
	}
	return conversation, true, nil
}

func (r *PostgresWhatsAppConversationRepository) FindMostRecentConversationByTenantAndDriverPhone(ctx context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, tenant_id, driver_phone, driver_name, last_inbound_message_at, last_outbound_message_at, last_template_sent_at, window_expires_at, last_message_at, last_message_preview, last_message_direction, last_message_type, last_read_at, created_at, updated_at, status, priority, assignee_user_id, tags, marked_unread
FROM whatsapp_conversations
WHERE tenant_id = $1 AND driver_phone = $2
ORDER BY updated_at DESC
LIMIT 1
`, strings.TrimSpace(tenantID), strings.TrimSpace(driverPhone))
	conversation, err := scanConversation(row)
	if err == sql.ErrNoRows {
		return domain.WhatsAppConversation{}, false, nil
	}
	if err != nil {
		return domain.WhatsAppConversation{}, false, err
	}
	return conversation, true, nil
}
func (r *PostgresWhatsAppConversationRepository) ListConversationsByTenant(ctx context.Context, tenantID string) ([]domain.WhatsAppConversation, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, tenant_id, driver_phone, driver_name, last_inbound_message_at, last_outbound_message_at, last_template_sent_at, window_expires_at, last_message_at, last_message_preview, last_message_direction, last_message_type, last_read_at, created_at, updated_at, status, priority, assignee_user_id, tags, marked_unread
FROM whatsapp_conversations
WHERE tenant_id = $1
ORDER BY COALESCE(last_message_at, created_at) DESC, updated_at DESC
`, strings.TrimSpace(tenantID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	conversations := make([]domain.WhatsAppConversation, 0)
	for rows.Next() {
		conversation, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

func (r *PostgresWhatsAppConversationRepository) SaveMessage(ctx context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error) {
	payload, err := json.Marshal(message.Payload)
	if err != nil {
		return domain.WhatsAppConversationMessage{}, fmt.Errorf("marshal whatsapp conversation message payload: %w", err)
	}
	providerMessageID := nullableString(strings.TrimSpace(message.ProviderMessageID))
	row := r.db.QueryRowContext(ctx, `
INSERT INTO whatsapp_conversation_messages (
    id,
    tenant_id,
    conversation_id,
    provider_message_id,
    client_message_id,
    direction,
    message_type,
    status,
    sender_role,
    sender_subject,
    from_phone,
    to_phone,
    text_content,
    template_name,
    media_type,
    media_id,
    media_link,
    caption,
    raw_payload,
    created_at,
    updated_at
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21
)
ON CONFLICT (provider_message_id) DO UPDATE SET
    status = EXCLUDED.status,
    raw_payload = EXCLUDED.raw_payload,
    updated_at = EXCLUDED.updated_at
WHERE whatsapp_conversation_messages.tenant_id = EXCLUDED.tenant_id
RETURNING
    id,
    tenant_id,
    conversation_id,
    provider_message_id,
    client_message_id,
    direction,
    message_type,
    status,
    sender_role,
    sender_subject,
    from_phone,
    to_phone,
    text_content,
    template_name,
    media_type,
    media_id,
    media_link,
    caption,
    raw_payload,
    created_at,
    updated_at
`,
		strings.TrimSpace(message.ID),
		strings.TrimSpace(message.TenantID),
		strings.TrimSpace(message.ConversationID),
		providerMessageID,
		nullableString(message.ClientMessageID),
		strings.TrimSpace(message.Direction),
		strings.TrimSpace(message.MessageType),
		strings.TrimSpace(message.Status),
		strings.TrimSpace(message.SenderRole),
		strings.TrimSpace(message.SenderSubject),
		strings.TrimSpace(message.FromPhone),
		strings.TrimSpace(message.ToPhone),
		strings.TrimSpace(message.Text),
		strings.TrimSpace(message.TemplateName),
		strings.TrimSpace(message.MediaType),
		strings.TrimSpace(message.MediaID),
		strings.TrimSpace(message.MediaLink),
		strings.TrimSpace(message.Caption),
		payload,
		message.CreatedAt.UTC(),
		message.UpdatedAt.UTC(),
	)
	saved, err := scanConversationMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WhatsAppConversationMessage{}, errors.New("provider message id already belongs to another tenant")
	}
	return saved, err
}

func (r *PostgresWhatsAppConversationRepository) ListMessagesByConversation(ctx context.Context, tenantID, conversationID string) ([]domain.WhatsAppConversationMessage, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, tenant_id, conversation_id, provider_message_id, client_message_id, direction, message_type, status, sender_role, sender_subject, from_phone, to_phone, text_content, template_name, media_type, media_id, media_link, caption, raw_payload, created_at, updated_at
FROM whatsapp_conversation_messages
WHERE tenant_id = $1 AND conversation_id = $2
ORDER BY created_at ASC, updated_at ASC
`, strings.TrimSpace(tenantID), strings.TrimSpace(conversationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]domain.WhatsAppConversationMessage, 0)
	for rows.Next() {
		message, err := scanConversationMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (r *PostgresWhatsAppConversationRepository) FindMessageByProviderMessageID(ctx context.Context, providerMessageID string) (domain.WhatsAppConversationMessage, bool, error) {
	trimmed := strings.TrimSpace(providerMessageID)
	if trimmed == "" {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	row := r.db.QueryRowContext(ctx, `
SELECT id, tenant_id, conversation_id, provider_message_id, client_message_id, direction, message_type, status, sender_role, sender_subject, from_phone, to_phone, text_content, template_name, media_type, media_id, media_link, caption, raw_payload, created_at, updated_at
FROM whatsapp_conversation_messages
WHERE provider_message_id = $1
`, trimmed)
	message, err := scanConversationMessage(row)
	if err == sql.ErrNoRows {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	if err != nil {
		return domain.WhatsAppConversationMessage{}, false, err
	}
	return message, true, nil
}

type sqlScanner interface{ Scan(dest ...any) error }

func scanConversation(scanner sqlScanner) (domain.WhatsAppConversation, error) {
	var conversation domain.WhatsAppConversation
	var lastInbound sql.NullTime
	var lastOutbound sql.NullTime
	var lastTemplate sql.NullTime
	var windowExpires sql.NullTime
	var lastMessage sql.NullTime
	var lastRead sql.NullTime
	var tags []byte
	if err := scanner.Scan(
		&conversation.ID,
		&conversation.TenantID,
		&conversation.DriverPhone,
		&conversation.DriverName,
		&lastInbound,
		&lastOutbound,
		&lastTemplate,
		&windowExpires,
		&lastMessage,
		&conversation.LastMessagePreview,
		&conversation.LastMessageDirection,
		&conversation.LastMessageType,
		&lastRead,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
		&conversation.Status,
		&conversation.Priority,
		&conversation.AssigneeUserID,
		&tags,
		&conversation.MarkedUnread,
	); err != nil {
		return domain.WhatsAppConversation{}, err
	}
	if len(tags) > 0 {
		_ = json.Unmarshal(tags, &conversation.Tags)
	}
	conversation.LastInboundMessageAt = timePtr(lastInbound)
	conversation.LastOutboundMessageAt = timePtr(lastOutbound)
	conversation.LastTemplateSentAt = timePtr(lastTemplate)
	conversation.WindowExpiresAt = timePtr(windowExpires)
	conversation.LastMessageAt = timePtr(lastMessage)
	conversation.LastReadAt = timePtr(lastRead)
	conversation.CreatedAt = conversation.CreatedAt.UTC()
	conversation.UpdatedAt = conversation.UpdatedAt.UTC()
	return conversation, nil
}

func scanConversationMessage(scanner sqlScanner) (domain.WhatsAppConversationMessage, error) {
	var message domain.WhatsAppConversationMessage
	var providerMessageID sql.NullString
	var clientMessageID sql.NullString
	var payload []byte
	if err := scanner.Scan(
		&message.ID,
		&message.TenantID,
		&message.ConversationID,
		&providerMessageID,
		&clientMessageID,
		&message.Direction,
		&message.MessageType,
		&message.Status,
		&message.SenderRole,
		&message.SenderSubject,
		&message.FromPhone,
		&message.ToPhone,
		&message.Text,
		&message.TemplateName,
		&message.MediaType,
		&message.MediaID,
		&message.MediaLink,
		&message.Caption,
		&payload,
		&message.CreatedAt,
		&message.UpdatedAt,
	); err != nil {
		return domain.WhatsAppConversationMessage{}, err
	}
	message.ProviderMessageID = providerMessageID.String
	message.ClientMessageID = clientMessageID.String
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &message.Payload)
	}
	if message.Payload == nil {
		message.Payload = map[string]any{}
	}
	message.CreatedAt = message.CreatedAt.UTC()
	message.UpdatedAt = message.UpdatedAt.UTC()
	return message, nil
}

func timePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

type InMemoryWhatsAppConversationRepository struct {
	mu               sync.RWMutex
	conversations    map[string]domain.WhatsAppConversation
	byTenantPhone    map[string]string
	messages         map[string]domain.WhatsAppConversationMessage
	providerMessages map[string]string
	clientMessages   map[string]string
}

func NewInMemoryWhatsAppConversationRepository() *InMemoryWhatsAppConversationRepository {
	return &InMemoryWhatsAppConversationRepository{
		conversations:    map[string]domain.WhatsAppConversation{},
		byTenantPhone:    map[string]string{},
		messages:         map[string]domain.WhatsAppConversationMessage{},
		providerMessages: map[string]string{},
		clientMessages:   map[string]string{},
	}
}

func (r *InMemoryWhatsAppConversationRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemoryWhatsAppConversationRepository) SaveConversation(_ context.Context, conversation domain.WhatsAppConversation) (domain.WhatsAppConversation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := tenantPhoneKey(conversation.TenantID, conversation.DriverPhone)
	if existingID, ok := r.byTenantPhone[key]; ok {
		existing := r.conversations[existingID]
		conversation.ID = existing.ID
		if strings.TrimSpace(conversation.DriverName) == "" {
			conversation.DriverName = existing.DriverName
		}
		if conversation.LastInboundMessageAt == nil {
			conversation.LastInboundMessageAt = cloneTime(existing.LastInboundMessageAt)
		}
		if conversation.LastOutboundMessageAt == nil {
			conversation.LastOutboundMessageAt = cloneTime(existing.LastOutboundMessageAt)
		}
		if conversation.LastTemplateSentAt == nil {
			conversation.LastTemplateSentAt = cloneTime(existing.LastTemplateSentAt)
		}
		if conversation.WindowExpiresAt == nil {
			conversation.WindowExpiresAt = cloneTime(existing.WindowExpiresAt)
		}
		if conversation.LastMessageAt == nil {
			conversation.LastMessageAt = cloneTime(existing.LastMessageAt)
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
		if conversation.LastReadAt == nil {
			conversation.LastReadAt = cloneTime(existing.LastReadAt)
		}
		if conversation.CreatedAt.IsZero() {
			conversation.CreatedAt = existing.CreatedAt
		}
		conversation = keepNewerActivity(existing, conversation)
		conversation.Status = reopenedStatus(existing, conversation)
		conversation.Priority = existing.Priority
		conversation.AssigneeUserID = existing.AssigneeUserID
		conversation.Tags = existing.Tags
		conversation.MarkedUnread = existing.MarkedUnread
	}
	if conversation.CreatedAt.IsZero() {
		conversation.CreatedAt = time.Now().UTC()
	}
	if conversation.UpdatedAt.IsZero() {
		conversation.UpdatedAt = conversation.CreatedAt
	}
	r.conversations[conversation.ID] = cloneConversation(conversation)
	r.byTenantPhone[key] = conversation.ID
	return cloneConversation(conversation), nil
}

func (r *InMemoryWhatsAppConversationRepository) FindConversationByTenantAndID(_ context.Context, tenantID, id string) (domain.WhatsAppConversation, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conversation, ok := r.conversations[strings.TrimSpace(id)]
	if !ok || conversation.TenantID != strings.TrimSpace(tenantID) {
		return domain.WhatsAppConversation{}, false, nil
	}
	return cloneConversation(conversation), true, nil
}

func (r *InMemoryWhatsAppConversationRepository) FindConversationByTenantAndDriverPhone(_ context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byTenantPhone[tenantPhoneKey(tenantID, driverPhone)]
	if !ok {
		return domain.WhatsAppConversation{}, false, nil
	}
	conversation := r.conversations[id]
	return cloneConversation(conversation), true, nil
}

func (r *InMemoryWhatsAppConversationRepository) FindMostRecentConversationByTenantAndDriverPhone(_ context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error) {
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
	return cloneConversation(latest), true, nil
}

func (r *InMemoryWhatsAppConversationRepository) ListConversationsByTenant(_ context.Context, tenantID string) ([]domain.WhatsAppConversation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conversations := make([]domain.WhatsAppConversation, 0)
	for _, conversation := range r.conversations {
		if conversation.TenantID == strings.TrimSpace(tenantID) {
			conversations = append(conversations, cloneConversation(conversation))
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

func (r *InMemoryWhatsAppConversationRepository) SaveMessage(_ context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(message.ProviderMessageID) != "" {
		if existingID, ok := r.providerMessages[strings.TrimSpace(message.ProviderMessageID)]; ok {
			existing := r.messages[existingID]
			message.ID = existing.ID
		}
	}
	if clientKey := clientMessageKey(message.TenantID, message.ClientMessageID); message.ClientMessageID != "" {
		if existingID, taken := r.clientMessages[clientKey]; taken && existingID != message.ID {
			return domain.WhatsAppConversationMessage{}, errDuplicateClientMessageID
		}
		r.clientMessages[clientKey] = message.ID
	}
	r.messages[message.ID] = cloneConversationMessage(message)
	if strings.TrimSpace(message.ProviderMessageID) != "" {
		r.providerMessages[strings.TrimSpace(message.ProviderMessageID)] = message.ID
	}
	return cloneConversationMessage(message), nil
}

func (r *InMemoryWhatsAppConversationRepository) ListMessagesByConversation(_ context.Context, tenantID, conversationID string) ([]domain.WhatsAppConversationMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	messages := make([]domain.WhatsAppConversationMessage, 0)
	for _, message := range r.messages {
		if message.TenantID == strings.TrimSpace(tenantID) && message.ConversationID == strings.TrimSpace(conversationID) {
			messages = append(messages, cloneConversationMessage(message))
		}
	}
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return messages, nil
}

func (r *InMemoryWhatsAppConversationRepository) FindMessageByProviderMessageID(_ context.Context, providerMessageID string) (domain.WhatsAppConversationMessage, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.providerMessages[strings.TrimSpace(providerMessageID)]
	if !ok {
		return domain.WhatsAppConversationMessage{}, false, nil
	}
	message := r.messages[id]
	return cloneConversationMessage(message), true, nil
}

func tenantPhoneKey(tenantID, driverPhone string) string {
	return strings.TrimSpace(tenantID) + "::" + strings.TrimSpace(driverPhone)
}

func cloneConversation(conversation domain.WhatsAppConversation) domain.WhatsAppConversation {
	conversation.Tags = append([]string(nil), conversation.Tags...)
	conversation.LastInboundMessageAt = cloneTime(conversation.LastInboundMessageAt)
	conversation.LastOutboundMessageAt = cloneTime(conversation.LastOutboundMessageAt)
	conversation.LastTemplateSentAt = cloneTime(conversation.LastTemplateSentAt)
	conversation.WindowExpiresAt = cloneTime(conversation.WindowExpiresAt)
	conversation.LastMessageAt = cloneTime(conversation.LastMessageAt)
	conversation.LastReadAt = cloneTime(conversation.LastReadAt)
	return conversation
}

func cloneConversationMessage(message domain.WhatsAppConversationMessage) domain.WhatsAppConversationMessage {
	if message.Payload != nil {
		payload := make(map[string]any, len(message.Payload))
		for key, value := range message.Payload {
			payload[key] = value
		}
		message.Payload = payload
	}
	return message
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

// reopenedStatus puts a resolved conversation back to open when the customer
// writes again.
func reopenedStatus(existing, saved domain.WhatsAppConversation) string {
	writtenAgain := saved.LastInboundMessageAt != nil && (existing.LastInboundMessageAt == nil || saved.LastInboundMessageAt.After(*existing.LastInboundMessageAt))
	if existing.Status == domain.ConversationStatusResolved && writtenAgain {
		return domain.ConversationStatusOpen
	}
	return existing.Status
}

// keepNewerActivity makes a save that was built before a later event unable
// to roll that event back, as the database upsert does with GREATEST.
func keepNewerActivity(existing, saved domain.WhatsAppConversation) domain.WhatsAppConversation {
	summaryStale := existing.LastMessageAt != nil && saved.LastMessageAt != nil && saved.LastMessageAt.Before(*existing.LastMessageAt)
	if summaryStale {
		saved.LastMessagePreview = existing.LastMessagePreview
		saved.LastMessageDirection = existing.LastMessageDirection
		saved.LastMessageType = existing.LastMessageType
	}
	saved.LastInboundMessageAt = laterOf(existing.LastInboundMessageAt, saved.LastInboundMessageAt)
	saved.LastOutboundMessageAt = laterOf(existing.LastOutboundMessageAt, saved.LastOutboundMessageAt)
	saved.LastTemplateSentAt = laterOf(existing.LastTemplateSentAt, saved.LastTemplateSentAt)
	saved.WindowExpiresAt = laterOf(existing.WindowExpiresAt, saved.WindowExpiresAt)
	saved.LastMessageAt = laterOf(existing.LastMessageAt, saved.LastMessageAt)
	return saved
}

func laterOf(a, b *time.Time) *time.Time {
	if a == nil {
		return cloneTime(b)
	}
	if b == nil || a.After(*b) {
		return cloneTime(a)
	}
	return cloneTime(b)
}
