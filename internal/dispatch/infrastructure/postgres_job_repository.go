package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const dispatchJobSchema = `
CREATE TABLE IF NOT EXISTS dispatch_jobs (
    tenant_id TEXT NOT NULL,
    id TEXT NOT NULL,
    reference TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    priority TEXT NOT NULL,
    pickup_address TEXT NOT NULL,
    dropoff_address TEXT NOT NULL,
    cargo_type TEXT NOT NULL DEFAULT '',
    cargo_description TEXT NOT NULL DEFAULT '',
    assigned_driver_id TEXT NOT NULL DEFAULT '',
    assigned_vehicle_id TEXT NOT NULL DEFAULT '',
    deadline TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    payload JSONB NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_dispatch_jobs_tenant_created_at
    ON dispatch_jobs (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_dispatch_jobs_tenant_status_created_at
    ON dispatch_jobs (tenant_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_dispatch_jobs_tenant_driver
    ON dispatch_jobs (tenant_id, assigned_driver_id)
    WHERE assigned_driver_id <> '';

CREATE INDEX IF NOT EXISTS idx_dispatch_jobs_tenant_vehicle
    ON dispatch_jobs (tenant_id, assigned_vehicle_id)
    WHERE assigned_vehicle_id <> '';
`

type PostgresJobRepository struct {
	db *sql.DB
}

func NewPostgresJobRepository(db *sql.DB) *PostgresJobRepository {
	return &PostgresJobRepository{db: db}
}

func (r *PostgresJobRepository) EnsureSchema(ctx context.Context) error {
	// Store the whole aggregate as JSONB while surfacing workflow fields in columns
	// so the dispatch service gains relational durability without changing its domain model.
	if _, err := r.db.ExecContext(ctx, dispatchJobSchema); err != nil {
		return fmt.Errorf("ensure dispatch job schema: %w", err)
	}
	return nil
}

func (r *PostgresJobRepository) Save(job domain.Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	var deadline any
	if job.Deadline != nil {
		deadline = job.Deadline.UTC()
	}

	_, err = r.db.ExecContext(
		context.Background(),
		`
INSERT INTO dispatch_jobs (
    tenant_id,
    id,
    reference,
    status,
    priority,
    pickup_address,
    dropoff_address,
    cargo_type,
    cargo_description,
    assigned_driver_id,
    assigned_vehicle_id,
    deadline,
    created_at,
    updated_at,
    payload
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
)
ON CONFLICT (tenant_id, id) DO UPDATE SET
    reference = EXCLUDED.reference,
    status = EXCLUDED.status,
    priority = EXCLUDED.priority,
    pickup_address = EXCLUDED.pickup_address,
    dropoff_address = EXCLUDED.dropoff_address,
    cargo_type = EXCLUDED.cargo_type,
    cargo_description = EXCLUDED.cargo_description,
    assigned_driver_id = EXCLUDED.assigned_driver_id,
    assigned_vehicle_id = EXCLUDED.assigned_vehicle_id,
    deadline = EXCLUDED.deadline,
    updated_at = EXCLUDED.updated_at,
    payload = EXCLUDED.payload
`,
		strings.TrimSpace(job.TenantID),
		strings.TrimSpace(job.ID),
		strings.TrimSpace(job.Reference),
		string(job.Status),
		string(job.Priority),
		strings.TrimSpace(job.Pickup.Address),
		strings.TrimSpace(job.Dropoff.Address),
		strings.TrimSpace(job.CargoType),
		strings.TrimSpace(job.CargoDescription),
		strings.TrimSpace(job.AssignedDriverID),
		strings.TrimSpace(job.AssignedVehicleID),
		deadline,
		job.CreatedAt.UTC(),
		job.UpdatedAt.UTC(),
		payload,
	)
	if err != nil {
		return fmt.Errorf("upsert dispatch job: %w", err)
	}

	return nil
}

func (r *PostgresJobRepository) FindByTenantAndID(tenantID, id string) (domain.Job, bool) {
	var payload []byte
	err := r.db.QueryRowContext(
		context.Background(),
		`SELECT payload FROM dispatch_jobs WHERE tenant_id = $1 AND id = $2`,
		strings.TrimSpace(tenantID),
		strings.TrimSpace(id),
	).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Job{}, false
		}
		return domain.Job{}, false
	}

	job, err := decodeStoredJob(payload)
	if err != nil {
		return domain.Job{}, false
	}
	return job, true
}

func (r *PostgresJobRepository) FindByTenant(tenantID string) []domain.Job {
	rows, err := r.db.QueryContext(
		context.Background(),
		`SELECT payload FROM dispatch_jobs WHERE tenant_id = $1 ORDER BY created_at ASC`,
		strings.TrimSpace(tenantID),
	)
	if err != nil {
		return []domain.Job{}
	}
	defer rows.Close()

	results := make([]domain.Job, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			continue
		}

		job, err := decodeStoredJob(payload)
		if err != nil {
			continue
		}
		results = append(results, job)
	}

	return results
}

func decodeStoredJob(payload []byte) (domain.Job, error) {
	var job domain.Job
	if err := json.Unmarshal(payload, &job); err != nil {
		return domain.Job{}, fmt.Errorf("unmarshal dispatch job: %w", err)
	}

	if job.Notes == nil {
		job.Notes = []domain.JobNote{}
	}
	if job.PODAttachments == nil {
		job.PODAttachments = []domain.JobPODAttachment{}
	}
	if job.Timeline == nil {
		job.Timeline = []domain.JobTimelineEntry{}
	}

	return job, nil
}
