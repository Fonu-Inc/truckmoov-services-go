package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

var _ application.DeadLetterRepository = (*PostgresDeadLetterRepository)(nil)

const deadLetterSchema = `
CREATE TABLE IF NOT EXISTS dead_letters (
    id TEXT PRIMARY KEY,
    original_subject TEXT NOT NULL,
    tenant_id TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL DEFAULT '',
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    failed_at TIMESTAMPTZ NOT NULL,
    envelope JSONB NOT NULL,
    replayed_at TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS idx_dead_letters_failed_at ON dead_letters (failed_at DESC);
`

const deadLetterColumns = `id, original_subject, tenant_id, event_type, attempts, last_error, failed_at, envelope, replayed_at`

// PostgresDeadLetterRepository keeps the platform's dead letters. They belong
// to no tenant, so reads and the replay claim are by id for platform staff;
// every row records the tenant of the event it holds.
type PostgresDeadLetterRepository struct {
	db *sql.DB
}

func NewPostgresDeadLetterRepository(db *sql.DB) *PostgresDeadLetterRepository {
	return &PostgresDeadLetterRepository{db: db}
}

func (r *PostgresDeadLetterRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, deadLetterSchema)
	return err
}

func (r *PostgresDeadLetterRepository) Save(ctx context.Context, letter application.DeadLetter) (bool, error) {
	envelope, err := json.Marshal(letter.Envelope)
	if err != nil {
		return false, fmt.Errorf("marshal dead letter envelope: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
        INSERT INTO dead_letters (id, original_subject, tenant_id, event_type, attempts, last_error, failed_at, envelope)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        ON CONFLICT (id) DO NOTHING`,
		letter.ID, letter.OriginalSubject, letter.TenantID, letter.EventType, letter.Attempts, letter.LastError, letter.FailedAt.UTC(), envelope)
	if err != nil {
		return false, err
	}
	created, err := result.RowsAffected()
	return created > 0, err
}

func scanDeadLetter(scanner interface{ Scan(...any) error }) (application.DeadLetter, error) {
	var letter application.DeadLetter
	var envelope []byte
	if err := scanner.Scan(&letter.ID, &letter.OriginalSubject, &letter.TenantID, &letter.EventType, &letter.Attempts, &letter.LastError, &letter.FailedAt, &envelope, &letter.ReplayedAt); err != nil {
		return application.DeadLetter{}, err
	}
	if err := json.Unmarshal(envelope, &letter.Envelope); err != nil {
		return application.DeadLetter{}, err
	}
	return letter, nil
}

func (r *PostgresDeadLetterRepository) Find(ctx context.Context, id string) (application.DeadLetter, bool, error) {
	letter, err := scanDeadLetter(r.db.QueryRowContext(ctx, `SELECT `+deadLetterColumns+` FROM dead_letters WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return application.DeadLetter{}, false, nil
	}
	if err != nil {
		return application.DeadLetter{}, false, err
	}
	return letter, true, nil
}

func (r *PostgresDeadLetterRepository) List(ctx context.Context, limit, offset int) ([]application.DeadLetter, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dead_letters`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+deadLetterColumns+` FROM dead_letters ORDER BY failed_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	letters := []application.DeadLetter{}
	for rows.Next() {
		letter, err := scanDeadLetter(rows)
		if err != nil {
			return nil, 0, err
		}
		letters = append(letters, letter)
	}
	return letters, total, rows.Err()
}

func (r *PostgresDeadLetterRepository) CountSince(ctx context.Context, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dead_letters WHERE failed_at >= $1`, since.UTC()).Scan(&count)
	return count, err
}

func (r *PostgresDeadLetterRepository) ClaimReplay(ctx context.Context, id string, at time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE dead_letters SET replayed_at = $2 WHERE id = $1 AND replayed_at IS NULL`, id, at.UTC())
	if err != nil {
		return false, err
	}
	claimed, err := result.RowsAffected()
	return claimed > 0, err
}

func (r *PostgresDeadLetterRepository) ReleaseReplay(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE dead_letters SET replayed_at = NULL WHERE id = $1`, id)
	return err
}

func (r *PostgresDeadLetterRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM dead_letters WHERE failed_at < $1`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PlatformAlertPublisher raises platform alerts on the bus. The bus signs
// every envelope it publishes, and the alert consumer verifies before acting.
type PlatformAlertPublisher struct {
	bus   eventbus.EventBus
	clock application.Clock
	ids   application.IDGenerator
}

var _ application.PlatformAlertRaiser = (*PlatformAlertPublisher)(nil)

func NewPlatformAlertPublisher(bus eventbus.EventBus, clock application.Clock, ids application.IDGenerator) *PlatformAlertPublisher {
	return &PlatformAlertPublisher{bus: bus, clock: clock, ids: ids}
}

func (p *PlatformAlertPublisher) Raise(ctx context.Context, alertType, severity, subject, body string) error {
	return p.bus.Publish(ctx, "tm.system.alert.raised", eventbus.EventEnvelope{
		ID:         p.ids.New("evt"),
		Type:       "alert.raised",
		Version:    1,
		OccurredAt: p.clock.Now().UTC(),
		TenantID:   platformMessagingTenant,
		Data: map[string]any{
			"alert_type": alertType,
			"severity":   severity,
			"subject":    subject,
			"body":       body,
		},
	})
}
