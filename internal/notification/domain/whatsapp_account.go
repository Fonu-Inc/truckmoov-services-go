package domain

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	WhatsAppTokenValid          = "valid"
	WhatsAppTokenNeedsReconnect = "needs_reconnect"

	WhatsAppStepOK      = "ok"
	WhatsAppStepFailed  = "failed"
	WhatsAppStepSkipped = "skipped"
)

type WhatsAppAccount struct {
	ID                   string     `json:"id"`
	TenantID             string     `json:"tenant_id"`
	PhoneNumberID        string     `json:"phone_number_id"`
	BusinessAccountID    string     `json:"business_account_id"`
	DisplayPhoneNumber   string     `json:"display_phone_number,omitempty"`
	VerifiedName         string     `json:"verified_name,omitempty"`
	BusinessName         string     `json:"business_name,omitempty"`
	QualityRating        string     `json:"quality_rating,omitempty"`
	NumberStatus         string     `json:"number_status,omitempty"`
	NameStatus           string     `json:"name_status,omitempty"`
	MessagingLimitTier   string     `json:"messaging_limit_tier,omitempty"`
	PlatformType         string     `json:"platform_type,omitempty"`
	Coexistence          bool       `json:"coexistence"`
	TokenStatus          string     `json:"token_status"`
	TokenExpiresAt       *time.Time `json:"token_expires_at,omitempty"`
	TokenNeverExpires    bool       `json:"token_never_expires"`
	StatusError          string     `json:"status_error,omitempty"`
	WebhookError         string     `json:"webhook_error,omitempty"`
	RegisterError        string     `json:"register_error,omitempty"`
	RegisteredAt         *time.Time `json:"registered_at,omitempty"`
	LastCheckedAt        *time.Time `json:"last_checked_at,omitempty"`
	WebhookSubscribedAt  *time.Time `json:"webhook_subscribed_at,omitempty"`
	ConnectedAt          time.Time  `json:"connected_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	EncryptedAccessToken string     `json:"-"`
	AccessTokenIV        string     `json:"-"`
	EncryptedPIN         string     `json:"-"`
	PINIV                string     `json:"-"`
	KeyID                string     `json:"-"`
}

type WhatsAppConnectionStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ConnectionSteps reads the four stages of a connection off the stored
// account, so a read after the fact reports the same as the connect did.
func (a WhatsAppAccount) ConnectionSteps() []WhatsAppConnectionStep {
	return []WhatsAppConnectionStep{
		a.tokenStep(),
		a.profileStep(),
		a.subscribedStep(),
		a.registeredStep(),
	}
}

func (a WhatsAppAccount) tokenStep() WhatsAppConnectionStep {
	if a.TokenStatus == WhatsAppTokenNeedsReconnect {
		return WhatsAppConnectionStep{Name: "token", Status: WhatsAppStepFailed, Error: "The access token is no longer valid. Reconnect the number."}
	}
	return WhatsAppConnectionStep{Name: "token", Status: WhatsAppStepOK}
}

func (a WhatsAppAccount) profileStep() WhatsAppConnectionStep {
	if strings.TrimSpace(a.StatusError) != "" {
		return WhatsAppConnectionStep{Name: "profile", Status: WhatsAppStepFailed, Error: a.StatusError}
	}
	return WhatsAppConnectionStep{Name: "profile", Status: WhatsAppStepOK}
}

func (a WhatsAppAccount) subscribedStep() WhatsAppConnectionStep {
	if a.WebhookSubscribedAt != nil {
		return WhatsAppConnectionStep{Name: "subscribed", Status: WhatsAppStepOK}
	}
	return WhatsAppConnectionStep{Name: "subscribed", Status: WhatsAppStepFailed, Error: firstNonEmpty(a.WebhookError, "Webhooks are not subscribed for this account.")}
}

func (a WhatsAppAccount) registeredStep() WhatsAppConnectionStep {
	switch {
	case a.Coexistence:
		return WhatsAppConnectionStep{Name: "registered", Status: WhatsAppStepSkipped}
	case a.RegisteredAt != nil:
		return WhatsAppConnectionStep{Name: "registered", Status: WhatsAppStepOK}
	default:
		return WhatsAppConnectionStep{Name: "registered", Status: WhatsAppStepFailed, Error: firstNonEmpty(a.RegisterError, "The number is not registered with WhatsApp Cloud API.")}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// MarshalJSON adds the connection steps; the sensitive fields stay out through
// their json:"-" tags.
func (a WhatsAppAccount) MarshalJSON() ([]byte, error) {
	type plain WhatsAppAccount
	return json.Marshal(struct {
		plain
		Steps []WhatsAppConnectionStep `json:"steps"`
	}{plain: plain(a), Steps: a.ConnectionSteps()})
}
