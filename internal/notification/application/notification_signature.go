package application

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

var (
	errSigningNotConfigured = errors.New("event signing secret is not configured")
	errBadSignature         = errors.New("notification event signature is missing or wrong")
	errSubjectTenant        = errors.New("notification event tenant does not match its subject")
)

const signatureSeparator = "\x1f"

func dataText(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

// SignatureFor must match notificationSignature in the TypeScript services.
func SignatureFor(secret string, envelope eventbus.EventEnvelope) string {
	payload := strings.Join([]string{
		"notification.send.v1",
		envelope.TenantID,
		envelope.ID,
		dataText(envelope.Data, "channel"),
		dataText(envelope.Data, "recipient"),
		dataText(envelope.Data, "notification_type"),
		dataText(envelope.Data, "content"),
	}, signatureSeparator)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyNotification refuses the event unless it was signed by a holder of the
// secret and its tenant is the one in the subject it arrived on. Anyone who can
// publish to the bus can pick the subject and the payload, so neither is
// trusted on its own.
func verifyNotification(secret string, envelope eventbus.EventEnvelope) error {
	if secret == "" {
		return errSigningNotConfigured
	}
	parts := strings.Split(envelope.Subject, ".")
	if len(parts) != 4 || parts[0] != "tm" || parts[1] == "" || parts[1] != envelope.TenantID {
		return errSubjectTenant
	}
	expected := SignatureFor(secret, envelope)
	if !hmac.Equal([]byte(expected), []byte(envelope.Signature)) {
		return errBadSignature
	}
	return nil
}
