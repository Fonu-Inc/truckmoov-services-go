package domain

import "time"

const (
	WhatsAppDirectionInbound  = "inbound"
	WhatsAppDirectionOutbound = "outbound"
	WhatsAppDirectionInternal = "internal"

	WhatsAppMessageTypeNote = "note"
)

type WhatsAppConversationMessage struct {
	ID                string         `json:"id"`
	TenantID          string         `json:"tenant_id"`
	ConversationID    string         `json:"conversation_id"`
	ProviderMessageID string         `json:"provider_message_id,omitempty"`
	ClientMessageID   string         `json:"client_message_id,omitempty"`
	Direction         string         `json:"direction"`
	MessageType       string         `json:"message_type"`
	Status            string         `json:"status,omitempty"`
	SenderRole        string         `json:"sender_role,omitempty"`
	SenderSubject     string         `json:"sender_subject,omitempty"`
	FromPhone         string         `json:"from_phone,omitempty"`
	ToPhone           string         `json:"to_phone,omitempty"`
	Text              string         `json:"text,omitempty"`
	TemplateName      string         `json:"template_name,omitempty"`
	MediaType         string         `json:"media_type,omitempty"`
	MediaID           string         `json:"media_id,omitempty"`
	MediaLink         string         `json:"media_link,omitempty"`
	Caption           string         `json:"caption,omitempty"`
	Payload           map[string]any `json:"payload,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

const WhatsAppMessageStatusScheduled = "scheduled"

// ScheduledAt reads the send time of a scheduled message.
func ScheduledAt(m WhatsAppConversationMessage) (time.Time, bool) {
	raw, _ := m.Payload["scheduled_at"].(string)
	at, err := time.Parse(time.RFC3339, raw)
	return at.UTC(), err == nil
}
