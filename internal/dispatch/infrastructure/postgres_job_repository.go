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
    assigned_driver_phone TEXT NOT NULL DEFAULT '',
    assigned_vehicle_id TEXT NOT NULL DEFAULT '',
    created_by TEXT NULL,
    deadline TIMESTAMPTZ NULL,
    delivered_at TIMESTAMPTZ NULL,
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
	if _, err := r.db.ExecContext(ctx, jobAttachmentSchema); err != nil {
		return fmt.Errorf("ensure job attachment schema: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
ALTER TABLE dispatch_jobs
    ADD COLUMN IF NOT EXISTS assigned_driver_phone TEXT NOT NULL DEFAULT ''
`); err != nil {
		return fmt.Errorf("ensure assigned driver phone column: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
ALTER TABLE dispatch_jobs
    ADD COLUMN IF NOT EXISTS created_by TEXT NULL
`); err != nil {
		return fmt.Errorf("ensure created by column: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
ALTER TABLE dispatch_jobs
    ADD COLUMN IF NOT EXISTS delivered_at TIMESTAMPTZ NULL
`); err != nil {
		return fmt.Errorf("ensure delivered at column: %w", err)
	}

	// Canonicalize older persisted priority values so the database, API responses,
	// and frontend all use the same vocabulary: low, normal, high, urgent.
	if _, err := r.db.ExecContext(ctx, `
UPDATE dispatch_jobs
SET
    priority = CASE
        WHEN lower(priority) = 'critical' THEN 'urgent'
        WHEN lower(priority) = 'medium' THEN 'normal'
        ELSE lower(priority)
    END,
    payload = jsonb_set(
        payload,
        '{priority}',
        to_jsonb(
            CASE
                WHEN lower(coalesce(payload->>'priority', '')) = 'critical' THEN 'urgent'
                WHEN lower(coalesce(payload->>'priority', '')) = 'medium' THEN 'normal'
                WHEN coalesce(payload->>'priority', '') = '' THEN CASE
                    WHEN lower(priority) = 'critical' THEN 'urgent'
                    WHEN lower(priority) = 'medium' THEN 'normal'
                    ELSE lower(priority)
                END
                ELSE lower(payload->>'priority')
            END
        ),
        true
    )
WHERE lower(priority) IN ('critical', 'medium')
   OR lower(coalesce(payload->>'priority', '')) IN ('critical', 'medium')
   OR coalesce(payload->>'priority', '') = ''
`); err != nil {
		return fmt.Errorf("normalize dispatch job priorities: %w", err)
	}
	return nil
}

func (r *PostgresJobRepository) Save(job domain.Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	var deadline, deliveredAt any
	if job.Deadline != nil {
		deadline = job.Deadline.UTC()
	}
	if job.DeliveredAt != nil {
		deliveredAt = job.DeliveredAt.UTC()
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
    assigned_driver_phone,
    assigned_vehicle_id,
    created_by,
    deadline,
    delivered_at,
    created_at,
    updated_at,
    payload
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
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
    assigned_driver_phone = EXCLUDED.assigned_driver_phone,
    assigned_vehicle_id = EXCLUDED.assigned_vehicle_id,
    deadline = EXCLUDED.deadline,
    delivered_at = EXCLUDED.delivered_at,
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
		strings.TrimSpace(job.AssignedDriverPhone),
		strings.TrimSpace(job.AssignedVehicleID),
		job.CreatedBy,
		deadline,
		deliveredAt,
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

func (r *PostgresJobRepository) FindByTenant(tenantID string) ([]domain.Job, error) {
	rows, err := r.db.QueryContext(
		context.Background(),
		`SELECT payload FROM dispatch_jobs WHERE tenant_id = $1 ORDER BY created_at ASC`,
		strings.TrimSpace(tenantID),
	)
	if err != nil {
		return nil, fmt.Errorf("find jobs by tenant: %w", err)
	}
	return scanJobRows(rows)
}

func (r *PostgresJobRepository) FindInProgressByTenant(ctx context.Context, tenantID string) ([]domain.Job, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT payload FROM dispatch_jobs WHERE tenant_id = $1 AND status NOT IN ('created', 'delivered', 'cancelled') ORDER BY created_at ASC`,
		strings.TrimSpace(tenantID),
	)
	if err != nil {
		return nil, fmt.Errorf("find in-progress jobs: %w", err)
	}
	return scanJobRows(rows)
}

func scanJobRows(rows *sql.Rows) ([]domain.Job, error) {
	defer rows.Close()

	jobs := make([]domain.Job, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan job row: %w", err)
		}
		job, err := decodeStoredJob(payload)
		if err != nil {
			return nil, fmt.Errorf("decode job payload: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job rows: %w", err)
	}
	return jobs, nil
}

// ListTenantsWithActiveJobs names every tenant with a job still in progress.
func (r *PostgresJobRepository) ListTenantsWithActiveJobs(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT tenant_id FROM dispatch_jobs WHERE status NOT IN ('created', 'delivered', 'cancelled')`)
	if err != nil {
		return nil, fmt.Errorf("list tenants with active jobs: %w", err)
	}
	defer rows.Close()

	tenants := make([]string, 0)
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenantID)
	}
	return tenants, rows.Err()
}

func (r *PostgresJobRepository) CountActive(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dispatch_jobs WHERE tenant_id = $1 AND status NOT IN ('delivered', 'cancelled')`,
		strings.TrimSpace(tenantID),
	).Scan(&count)
	return count, err
}

func (r *PostgresJobRepository) Delete(tenantID, id string) error {
	tenantID, id = strings.TrimSpace(tenantID), strings.TrimSpace(id)
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete dispatch job: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM dispatch_job_attachments WHERE tenant_id = $1 AND job_id = $2`, tenantID, id); err != nil {
		return fmt.Errorf("delete dispatch job attachments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dispatch_jobs WHERE tenant_id = $1 AND id = $2`, tenantID, id); err != nil {
		return fmt.Errorf("delete dispatch job: %w", err)
	}
	return tx.Commit()
}

func decodeStoredJob(payload []byte) (domain.Job, error) {
	var job domain.Job
	if err := json.Unmarshal(payload, &job); err != nil {
		return domain.Job{}, fmt.Errorf("unmarshal dispatch job: %w", err)
	}

	priority, err := domain.NormalizePriority(string(job.Priority))
	if err != nil {
		return domain.Job{}, fmt.Errorf("normalize dispatch job priority: %w", err)
	}
	job.Priority = priority

	if job.Notes == nil {
		job.Notes = []domain.JobNote{}
	}
	if job.PODAttachments == nil {
		job.PODAttachments = []domain.JobPODAttachment{}
	}
	if job.DriverStatusRequests == nil {
		job.DriverStatusRequests = []domain.DriverStatusRequest{}
	}
	if job.Timeline == nil {
		job.Timeline = []domain.JobTimelineEntry{}
	}
	if job.Attachments == nil {
		job.Attachments = []domain.JobAttachment{}
	}

	return job, nil
}
