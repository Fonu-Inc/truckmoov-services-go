package eventbus

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
)

var (
	ErrSecretUnset    = errors.New("event signing secret is not configured")
	ErrBadSubject     = errors.New("event subject is not tm.<tenant>.<event>")
	ErrTenantMismatch = errors.New("event tenant does not match its subject")
	ErrUnsigned       = errors.New("event carries no bus signature")
	ErrBadSignature   = errors.New("event bus signature is wrong")
)

const busSeparator = "\x1f"

type VerifyOptions struct {
	AcceptUnsigned bool
}

func digest(v any) (string, error) {
	canonical, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func objectOrEmpty(m map[string]any) any {
	if len(m) == 0 {
		return map[string]any{}
	}
	return m
}

// BusSignature covers subject, id, type, tenant, data and actor. occurred_at is
// not signed, so a captured event can be replayed: consumers with side effects
// dedupe by id. Must match busSignature in the TypeScript eventbus library.
func BusSignature(secret, subject string, e EventEnvelope) (string, error) {
	data, err := digest(objectOrEmpty(e.Data))
	if err != nil {
		return "", err
	}
	actor, err := digest(objectOrEmpty(e.Actor))
	if err != nil {
		return "", err
	}
	payload := strings.Join([]string{"bus.v2", subject, e.ID, e.Type, e.TenantID, data, actor}, busSeparator)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func subjectTenant(subject string) (string, bool) {
	parts := strings.Split(subject, ".")
	if len(parts) < 3 || parts[0] != "tm" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

// VerifyBus checks the event against the subject it was delivered on. A present
// but wrong signature is always refused; a missing one only with AcceptUnsigned.
func VerifyBus(secret string, e EventEnvelope, opts VerifyOptions) error {
	if secret == "" {
		return ErrSecretUnset
	}
	tenant, ok := subjectTenant(e.Subject)
	if !ok {
		return ErrBadSubject
	}
	if e.TenantID != tenant {
		return ErrTenantMismatch
	}
	if e.BusSignature == "" {
		if opts.AcceptUnsigned {
			return nil
		}
		return ErrUnsigned
	}
	expected, err := BusSignature(secret, e.Subject, e)
	if err != nil || !hmac.Equal([]byte(expected), []byte(e.BusSignature)) {
		return ErrBadSignature
	}
	return nil
}

// Verified drops events that fail verification. It returns nil for them so the
// bus acknowledges instead of redelivering a forged event forever.
func Verified(handler Handler, secret string, opts VerifyOptions, log *slog.Logger) Handler {
	if secret == "" {
		log.Error("event_signing_secret_unset", "effect", "every verified event is rejected")
	}
	return func(ctx context.Context, e EventEnvelope) error {
		if err := VerifyBus(secret, e, opts); err != nil {
			log.Warn("event_rejected", "subject", e.Subject, "reason", err.Error())
			return nil
		}
		if e.BusSignature == "" {
			log.Warn("event_unsigned_accepted", "subject", e.Subject)
		}
		return handler(ctx, e)
	}
}
