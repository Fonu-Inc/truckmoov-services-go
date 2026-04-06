package domain

import (
	"math"
	"time"
)

type WhatsAppConversation struct {
	ID                    string     `json:"id"`
	TenantID              string     `json:"tenant_id"`
	DriverPhone           string     `json:"driver_phone"`
	DriverName            string     `json:"driver_name,omitempty"`
	LastInboundMessageAt  *time.Time `json:"last_inbound_message_at,omitempty"`
	LastOutboundMessageAt *time.Time `json:"last_outbound_message_at,omitempty"`
	LastTemplateSentAt    *time.Time `json:"last_template_sent_at,omitempty"`
	WindowExpiresAt       *time.Time `json:"window_expires_at,omitempty"`
	LastMessageAt         *time.Time `json:"last_message_at,omitempty"`
	LastMessagePreview    string     `json:"last_message_preview,omitempty"`
	LastMessageDirection  string     `json:"last_message_direction,omitempty"`
	LastMessageType       string     `json:"last_message_type,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

func (c WhatsAppConversation) WindowRemainingSeconds(now time.Time) int64 {
	if c.WindowExpiresAt == nil || c.WindowExpiresAt.IsZero() {
		return 0
	}
	remaining := c.WindowExpiresAt.UTC().Sub(now.UTC())
	if remaining <= 0 {
		return 0
	}
	return int64(remaining.Seconds())
}

func (c WhatsAppConversation) WindowRemainingHours(now time.Time) float64 {
	seconds := c.WindowRemainingSeconds(now)
	if seconds == 0 {
		return 0
	}
	hours := float64(seconds) / 3600
	return math.Round(hours*100) / 100
}

func (c WhatsAppConversation) IsLocked(now time.Time) bool {
	return c.WindowRemainingSeconds(now) == 0
}
