package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/identity"
)

const (
	DeadLetterSubject = "tm.system.dlq.>"

	deadLetterEventType     = "system.dlq"
	deadLetterPrefix        = "tm.system.dlq."
	deadLetterRetention     = 30 * 24 * time.Hour
	deadLetterFloodWindow   = time.Hour
	deadLetterFloodLimit    = 10
	deadLetterFloodClaimTTL = 2 * time.Hour
	deadLetterPageSize      = 50
)

var (
	ErrDeadLetterNotFound        = errors.New("dead letter not found")
	ErrDeadLetterAlreadyReplayed = errors.New("this dead letter was already replayed")
	ErrDeadLetterNotReplayable   = errors.New("this dead letter cannot be replayed")
)

// DeadLetter is an event the bus gave up delivering. The envelope is kept as
// it was published so a replay sends the same event.
type DeadLetter struct {
	ID              string
	OriginalSubject string
	TenantID        string
	EventType       string
	Attempts        int
	LastError       string
	FailedAt        time.Time
	Envelope        map[string]any
	ReplayedAt      *time.Time
}

// DeadLetterRepository holds the platform's dead letters. They are not a
// tenant's records: staff read and replay them across tenants, and each row
// names the tenant of the event it holds.
type DeadLetterRepository interface {
	EnsureSchema(ctx context.Context) error
	Save(ctx context.Context, letter DeadLetter) (bool, error)
	Find(ctx context.Context, id string) (DeadLetter, bool, error)
	List(ctx context.Context, limit, offset int) ([]DeadLetter, int, error)
	CountSince(ctx context.Context, since time.Time) (int, error)
	// ClaimReplay marks the letter replayed, and reports false when it
	// already was, so two clicks never send the event twice.
	ClaimReplay(ctx context.Context, id string, at time.Time) (bool, error)
	ReleaseReplay(ctx context.Context, id string) error
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

type DeadLetterPublisher interface {
	Publish(ctx context.Context, subject string, envelope eventbus.EventEnvelope) error
}

// PlatformAlertRaiser tells platform staff something needs a look.
type PlatformAlertRaiser interface {
	Raise(ctx context.Context, alertType, severity, subject, body string) error
}

type DeadLetters struct {
	repository DeadLetterRepository
	publisher  DeadLetterPublisher
	alerts     PlatformAlertRaiser
	claims     EventClaimStore
	clock      Clock
	log        *slog.Logger
}

func NewDeadLetters(repository DeadLetterRepository, publisher DeadLetterPublisher, alerts PlatformAlertRaiser, claims EventClaimStore, clock Clock, log *slog.Logger) *DeadLetters {
	return &DeadLetters{repository: repository, publisher: publisher, alerts: alerts, claims: claims, clock: clock, log: log}
}

// Handle stores one dead letter. The event must have passed bus signature
// verification, and only the platform's own subject is accepted.
func (u *DeadLetters) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	if envelope.Type != deadLetterEventType || envelope.TenantID != identity.SystemTenantID || !strings.HasPrefix(envelope.Subject, deadLetterPrefix) || envelope.ID == "" {
		u.log.Warn("dead_letter_invalid", "subject", envelope.Subject, "event", envelope.ID)
		return nil
	}
	letter, ok := parseDeadLetter(envelope)
	if !ok {
		u.log.Warn("dead_letter_unreadable", "event", envelope.ID)
		return nil
	}
	created, err := u.repository.Save(ctx, letter)
	if err != nil {
		return fmt.Errorf("store dead letter %s: %w", letter.ID, err)
	}
	if created {
		return u.alertOnFlood(ctx)
	}
	return nil
}

func parseDeadLetter(envelope eventbus.EventEnvelope) (DeadLetter, bool) {
	original, _ := envelope.Data["original"].(map[string]any)
	subject, _ := envelope.Data["original_subject"].(string)
	if original == nil || strings.TrimSpace(subject) == "" {
		return DeadLetter{}, false
	}
	failedAt, err := time.Parse(time.RFC3339, dataText(envelope.Data, "failed_at"))
	if err != nil {
		failedAt = envelope.OccurredAt
	}
	attempts, _ := envelope.Data["attempts"].(float64)
	return DeadLetter{
		ID:              envelope.ID,
		OriginalSubject: subject,
		TenantID:        dataText(original, "tenant_id"),
		EventType:       dataText(original, "type"),
		Attempts:        int(attempts),
		LastError:       dataText(envelope.Data, "last_error"),
		FailedAt:        failedAt.UTC(),
		Envelope:        original,
	}, true
}

// alertOnFlood raises one platform alert per hour while dead letters keep
// arriving faster than the limit, because a burst means something is broken
// rather than one event being unlucky.
func (u *DeadLetters) alertOnFlood(ctx context.Context) error {
	now := u.clock.Now()
	count, err := u.repository.CountSince(ctx, now.Add(-deadLetterFloodWindow))
	if err != nil {
		return fmt.Errorf("count dead letters: %w", err)
	}
	if count <= deadLetterFloodLimit {
		return nil
	}
	key := fmt.Sprintf("dead_letter_flood|%d", now.Unix()/int64(deadLetterFloodWindow.Seconds()))
	claimed, err := u.claims.Claim(ctx, key, deadLetterFloodClaimTTL)
	if err != nil || !claimed {
		return err
	}
	body := fmt.Sprintf("%d events were dead-lettered in the last hour. Open the dead letters list to see what failed.", count)
	if err := u.alerts.Raise(ctx, "system_errors", "high", "Many events are failing to process", body); err != nil {
		_ = u.claims.Release(ctx, key)
		return fmt.Errorf("raise dead letter alert: %w", err)
	}
	return nil
}

func (u *DeadLetters) List(ctx context.Context, page int) ([]DeadLetter, int, error) {
	if page < 1 {
		page = 1
	}
	return u.repository.List(ctx, deadLetterPageSize, (page-1)*deadLetterPageSize)
}

// Replay publishes the original event again, signed by the bus, to the subject
// it first went to. It works once per dead letter.
func (u *DeadLetters) Replay(ctx context.Context, id string) (DeadLetter, error) {
	letter, found, err := u.repository.Find(ctx, strings.TrimSpace(id))
	if err != nil {
		return DeadLetter{}, err
	}
	if !found {
		return DeadLetter{}, ErrDeadLetterNotFound
	}
	original, subject, err := replayableEnvelope(letter)
	if err != nil {
		return DeadLetter{}, err
	}
	now := u.clock.Now()
	claimed, err := u.repository.ClaimReplay(ctx, letter.ID, now)
	if err != nil {
		return DeadLetter{}, err
	}
	if !claimed {
		return DeadLetter{}, ErrDeadLetterAlreadyReplayed
	}
	if err := u.publisher.Publish(ctx, subject, original); err != nil {
		return DeadLetter{}, errors.Join(fmt.Errorf("replay dead letter %s: %w", letter.ID, err), u.repository.ReleaseReplay(ctx, letter.ID))
	}
	letter.ReplayedAt = &now
	return letter, nil
}

func replayableEnvelope(letter DeadLetter) (eventbus.EventEnvelope, string, error) {
	subject := strings.TrimSpace(letter.OriginalSubject)
	if !strings.HasPrefix(subject, "tm.") || strings.HasPrefix(subject, deadLetterPrefix) || strings.Count(subject, ".") < 2 {
		return eventbus.EventEnvelope{}, "", ErrDeadLetterNotReplayable
	}
	raw, err := json.Marshal(letter.Envelope)
	if err != nil {
		return eventbus.EventEnvelope{}, "", ErrDeadLetterNotReplayable
	}
	var envelope eventbus.EventEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.ID == "" {
		return eventbus.EventEnvelope{}, "", ErrDeadLetterNotReplayable
	}
	return envelope, subject, nil
}

// Purge drops dead letters past retention.
func (u *DeadLetters) Purge(ctx context.Context) (int64, error) {
	return u.repository.DeleteOlderThan(ctx, u.clock.Now().Add(-deadLetterRetention))
}
