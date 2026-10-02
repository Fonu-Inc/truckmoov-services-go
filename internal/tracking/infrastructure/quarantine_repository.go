package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

const quarantineSchema = `
CREATE TABLE IF NOT EXISTS tracking_quarantine (
    imei TEXT PRIMARY KEY,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    message_count BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active',
    handled_at TIMESTAMPTZ NULL
);

ALTER TABLE tracking_quarantine ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE tracking_quarantine ADD COLUMN IF NOT EXISTS handled_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_tracking_quarantine_last_seen
    ON tracking_quarantine (last_seen_at DESC);
`

type PostgresQuarantineRepository struct {
	db *sql.DB
}

func NewPostgresQuarantineRepository(db *sql.DB) *PostgresQuarantineRepository {
	return &PostgresQuarantineRepository{db: db}
}

func (r *PostgresQuarantineRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, quarantineSchema)
	return err
}

func (r *PostgresQuarantineRepository) Record(ctx context.Context, imei string, count int, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO tracking_quarantine (imei, first_seen_at, last_seen_at, message_count)
VALUES ($1, $2, $2, $3)
ON CONFLICT (imei) DO UPDATE
SET last_seen_at = EXCLUDED.last_seen_at,
    message_count = tracking_quarantine.message_count + EXCLUDED.message_count`,
		imei, at.UTC(), count)
	return err
}

func (r *PostgresQuarantineRepository) List(ctx context.Context, limit int) ([]application.QuarantinedDevice, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT imei, first_seen_at, last_seen_at, message_count, status, handled_at
FROM tracking_quarantine
ORDER BY last_seen_at DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := []application.QuarantinedDevice{}
	for rows.Next() {
		var device application.QuarantinedDevice
		if err := rows.Scan(&device.IMEI, &device.FirstSeenAt, &device.LastSeenAt, &device.MessageCount, &device.Status, &device.HandledAt); err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func (r *PostgresQuarantineRepository) Remove(ctx context.Context, imei string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM tracking_quarantine WHERE imei = $1`, imei)
	return err
}

func (r *PostgresQuarantineRepository) SetStatus(ctx context.Context, imei, status string, at time.Time) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE tracking_quarantine SET status = $2, handled_at = $3 WHERE imei = $1 AND `+statusGuard(status),
		imei, status, at.UTC())
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed > 0 {
		return nil
	}
	return r.explainRefusal(ctx, imei, status)
}

func statusGuard(status string) string {
	switch status {
	case application.QuarantineDismissed:
		return `status <> 'blocked'`
	case application.QuarantineActive:
		return `status = 'blocked'`
	}
	return `TRUE`
}

func (r *PostgresQuarantineRepository) explainRefusal(ctx context.Context, imei, status string) error {
	var current string
	err := r.db.QueryRowContext(ctx, `SELECT status FROM tracking_quarantine WHERE imei = $1`, imei).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return application.ErrQuarantineNotFound
	}
	if err != nil {
		return err
	}
	return refusalFor(current, status)
}

func refusalFor(current, status string) error {
	if status == application.QuarantineDismissed && current == application.QuarantineBlocked {
		return application.ErrQuarantineBlocked
	}
	if status == application.QuarantineActive && current != application.QuarantineBlocked {
		return application.ErrQuarantineNotBlocked
	}
	return nil
}

func (r *PostgresQuarantineRepository) BlockedIMEIs(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT imei FROM tracking_quarantine WHERE status = $1`, application.QuarantineBlocked)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	imeis := []string{}
	for rows.Next() {
		var imei string
		if err := rows.Scan(&imei); err != nil {
			return nil, err
		}
		imeis = append(imeis, imei)
	}
	return imeis, rows.Err()
}

func (r *PostgresQuarantineRepository) Prune(ctx context.Context, cutoff time.Time) (int, error) {
	result, err := r.db.ExecContext(ctx, `
DELETE FROM tracking_quarantine
WHERE status <> $1
  AND (last_seen_at < $2 OR (status = $3 AND handled_at < $2))`,
		application.QuarantineBlocked, cutoff.UTC(), application.QuarantineDismissed)
	if err != nil {
		return 0, err
	}
	removed, _ := result.RowsAffected()
	return int(removed), nil
}

type InMemoryQuarantineRepository struct {
	mu      sync.Mutex
	entries map[string]application.QuarantinedDevice
}

func NewInMemoryQuarantineRepository() *InMemoryQuarantineRepository {
	return &InMemoryQuarantineRepository{entries: map[string]application.QuarantinedDevice{}}
}

func (r *InMemoryQuarantineRepository) Record(_ context.Context, imei string, count int, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[imei]
	if !ok {
		entry = application.QuarantinedDevice{IMEI: imei, FirstSeenAt: at, Status: application.QuarantineActive}
	}
	entry.LastSeenAt = at
	entry.MessageCount += count
	r.entries[imei] = entry
	return nil
}

func (r *InMemoryQuarantineRepository) List(_ context.Context, limit int) ([]application.QuarantinedDevice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	devices := make([]application.QuarantinedDevice, 0, len(r.entries))
	for _, entry := range r.entries {
		devices = append(devices, entry)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].LastSeenAt.After(devices[j].LastSeenAt) })
	if limit > 0 && len(devices) > limit {
		devices = devices[:limit]
	}
	return devices, nil
}

func (r *InMemoryQuarantineRepository) Remove(_ context.Context, imei string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, imei)
	return nil
}

func (r *InMemoryQuarantineRepository) SetStatus(_ context.Context, imei, status string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[imei]
	if !ok {
		return application.ErrQuarantineNotFound
	}
	if err := refusalFor(entry.Status, status); err != nil {
		return err
	}
	entry.Status = status
	entry.HandledAt = &at
	r.entries[imei] = entry
	return nil
}

func (r *InMemoryQuarantineRepository) BlockedIMEIs(_ context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	imeis := []string{}
	for imei, entry := range r.entries {
		if entry.Status == application.QuarantineBlocked {
			imeis = append(imeis, imei)
		}
	}
	return imeis, nil
}

func (r *InMemoryQuarantineRepository) Prune(_ context.Context, cutoff time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for imei, entry := range r.entries {
		if entry.Status == application.QuarantineBlocked {
			continue
		}
		stale := entry.LastSeenAt.Before(cutoff)
		handled := entry.Status == application.QuarantineDismissed && entry.HandledAt != nil && entry.HandledAt.Before(cutoff)
		if stale || handled {
			delete(r.entries, imei)
			removed++
		}
	}
	return removed, nil
}
