package domain

import (
	"errors"
	"strings"
	"time"
)

// NotificationPreferences is which channels a tenant wants its own alerts on,
// such as a job offer made to it. Email is on until the tenant says otherwise.
type NotificationPreferences struct {
	TenantID  string    `json:"tenant_id"`
	Email     bool      `json:"email"`
	WhatsApp  bool      `json:"whatsapp"`
	SMS       bool      `json:"sms"`
	UpdatedAt time.Time `json:"updated_at"`
}

func DefaultNotificationPreferences(tenantID string) NotificationPreferences {
	return NotificationPreferences{TenantID: strings.TrimSpace(tenantID), Email: true}
}

// ErrNoNotificationChannel is returned when every channel is switched off,
// which would make the tenant miss an offer without knowing it.
var ErrNoNotificationChannel = errors.New("choose at least one way to be told about job offers")

func (p NotificationPreferences) Validate() error {
	if strings.TrimSpace(p.TenantID) == "" {
		return errors.New("tenant id is required")
	}
	if !p.Email && !p.WhatsApp && !p.SMS {
		return ErrNoNotificationChannel
	}
	return nil
}
