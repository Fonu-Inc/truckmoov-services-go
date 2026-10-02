package eventbus

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	dlqPrefix = "tm.system.dlq."
	dlqTenant = "system"
	dlqType   = "system.dlq"
)

func newEventID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// deadLetterSubject is tm.system.dlq.<event part of the original subject>,
// inside the stream's tm.*.> capture. Dead letters are never dead-lettered.
func deadLetterSubject(original string) (string, bool) {
	parts := strings.Split(original, ".")
	if len(parts) < 3 || strings.HasPrefix(original, dlqPrefix) {
		return "", false
	}
	return dlqPrefix + strings.Join(parts[2:], "."), true
}

func deadLetterEnvelope(original EventEnvelope, subject string, attempts uint64, lastErr error, at time.Time) EventEnvelope {
	reason := ""
	if lastErr != nil {
		reason = lastErr.Error()
	}
	return EventEnvelope{
		ID:         newEventID(),
		Type:       dlqType,
		Version:    1,
		OccurredAt: at,
		TenantID:   dlqTenant,
		Data: map[string]any{
			"original_subject": subject,
			"attempts":         attempts,
			"last_error":       reason,
			"failed_at":        at.UTC().Format(time.RFC3339),
			"original":         original,
		},
	}
}

func (b *natsEventBus) deadLetter(msgSubject string, original EventEnvelope, attempts uint64, lastErr error) error {
	subject, ok := deadLetterSubject(msgSubject)
	if !ok {
		return fmt.Errorf("no dead-letter subject for %q", msgSubject)
	}
	envelope := b.sign(subject, deadLetterEnvelope(original, msgSubject, attempts, lastErr, time.Now()))
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	_, err = b.js.Publish(subject, payload)
	return err
}
