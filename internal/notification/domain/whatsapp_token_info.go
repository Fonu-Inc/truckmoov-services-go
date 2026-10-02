package domain

import (
	"errors"
	"time"
)

var ErrWhatsAppNumberInUse = errors.New("this WhatsApp number is already connected to another account")

type WhatsAppTokenInfo struct {
	Valid        bool
	NeverExpires bool
	ExpiresAt    *time.Time
	// BusinessAccountIDs are the WhatsApp Business Accounts the token was
	// granted, as Meta lists them in the token's granular scopes.
	BusinessAccountIDs []string
}
