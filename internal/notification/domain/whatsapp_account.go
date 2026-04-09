package domain

import "time"

type WhatsAppAccount struct {
	ID                   string     `json:"id"`
	TenantID             string     `json:"tenant_id"`
	PhoneNumberID        string     `json:"phone_number_id"`
	BusinessAccountID    string     `json:"business_account_id"`
	DisplayPhoneNumber   string     `json:"display_phone_number,omitempty"`
	VerifiedName         string     `json:"verified_name,omitempty"`
	BusinessName         string     `json:"business_name,omitempty"`
	EncryptedAccessToken string     `json:"-"`
	AccessTokenIV        string     `json:"-"`
	WebhookSubscribedAt  *time.Time `json:"webhook_subscribed_at,omitempty"`
	ConnectedAt          time.Time  `json:"connected_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}
