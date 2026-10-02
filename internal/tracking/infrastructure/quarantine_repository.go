package infrastructure

import (
	"context"
	"database/sql"
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
    message_count BIGINT NOT NULL DEFAULT 0
);

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
SELECT imei, first_seen_at, last_seen_at, message_count
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
		if err := rows.Scan(&device.IMEI, &device.FirstSeenAt, &device.LastSeenAt, &device.MessageCount); err != nil {
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
		entry = application.QuarantinedDevice{IMEI: imei, FirstSeenAt: at}
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
