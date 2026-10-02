package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type WhatsAppAPIError struct {
	HTTPStatus int
	Code       int
	Subcode    int
	Type       string
	Message    string
	Body       string
}

func (e *WhatsAppAPIError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("whatsapp api %d: %s", e.HTTPStatus, e.Message)
	}
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("whatsapp api %d", e.HTTPStatus)
	}
	return fmt.Sprintf("whatsapp api %d: %s", e.HTTPStatus, strings.TrimSpace(e.Body))
}

func (e *WhatsAppAPIError) IsTokenInvalid() bool {
	return e.Code == 190
}

func (e *WhatsAppAPIError) IsRateLimited() bool {
	switch e.Code {
	case 4, 17, 32, 613, 80007, 130429:
		return true
	}
	return false
}

// PlainMessage is what staff read: the Meta text is kept only when no known
// code explains the failure.
func (e *WhatsAppAPIError) PlainMessage() string {
	switch {
	case e.IsTokenInvalid():
		return "Meta rejected the access token. Reconnect the number."
	case e.IsRateLimited():
		return "Meta is rate limiting requests. Try again in a few minutes."
	case e.Code == 100 && (e.Subcode == 36007 || e.Subcode == 36008 || e.Subcode == 36009):
		return "The signup code expired or was already used. Start the connection again."
	case e.Code == 101 || strings.Contains(strings.ToLower(e.Message), "client secret"):
		return "Meta rejected the app credentials. Check WHATSAPP_APP_ID and WHATSAPP_APP_SECRET match the Meta app."
	case e.Code == 133005:
		return "The number is registered with a different two-step PIN. Reset it in WhatsApp Manager, then register again."
	case e.Code == 133016:
		return "Too many registration attempts. Wait before trying again."
	case e.Code == 133010:
		return "The number is not registered with WhatsApp Cloud API."
	case e.Code == 10 || (e.Code >= 200 && e.Code < 300):
		return "The Meta app lacks permission for this WhatsApp account."
	}
	if strings.TrimSpace(e.Message) != "" {
		return strings.TrimSpace(e.Message)
	}
	return e.Error()
}

// PlainErrorText turns any provider error into text safe to show staff.
func PlainErrorText(err error) string {
	if err == nil {
		return ""
	}
	if apiErr, ok := AsWhatsAppAPIError(err); ok {
		return apiErr.PlainMessage()
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "Could not reach Meta. Try again."
	}
	return err.Error()
}

func AsWhatsAppAPIError(err error) (*WhatsAppAPIError, bool) {
	var apiErr *WhatsAppAPIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}
