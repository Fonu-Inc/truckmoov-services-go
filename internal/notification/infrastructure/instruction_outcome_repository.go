package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
)

var _ application.InstructionOutcomeOutbox = (*PostgresInstructionOutcomeRepository)(nil)

const instructionOutcomeSchema = `
CREATE TABLE IF NOT EXISTS job_instruction_outcomes (
    tenant_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    driver_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    channel TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ NOT NULL,
    claimed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, job_id, event_id)
);
CREATE INDEX IF NOT EXISTS idx_job_instruction_outcomes_due
    ON job_instruction_outcomes (next_attempt_at) WHERE status = 'pending';
`

// PostgresInstructionOutcomeRepository is the outbox of driver instruction
// outcomes dispatch has not accepted yet.
type PostgresInstructionOutcomeRepository struct {
	db *sql.DB
}

func NewPostgresInstructionOutcomeRepository(db *sql.DB) *PostgresInstructionOutcomeRepository {
	return &PostgresInstructionOutcomeRepository{db: db}
}

func (r *PostgresInstructionOutcomeRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, instructionOutcomeSchema)
	return err
}

func (r *PostgresInstructionOutcomeRepository) Enqueue(ctx context.Context, report application.DriverInstructionReport, at time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
        INSERT INTO job_instruction_outcomes
            (tenant_id, job_id, event_id, driver_id, outcome, channel, reason, status, next_attempt_at, claimed_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8,$8,$8,$8)
        ON CONFLICT (tenant_id, job_id, event_id) DO NOTHING`,
		report.TenantID, report.JobID, report.EventID, report.DriverID, report.Outcome, report.Channel, report.Reason, at.UTC())
	if err != nil {
		return false, fmt.Errorf("enqueue instruction outcome: %w", err)
	}
	created, err := result.RowsAffected()
	return created > 0, err
}

func (r *PostgresInstructionOutcomeRepository) MarkDelivered(ctx context.Context, report application.DriverInstructionReport, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
        UPDATE job_instruction_outcomes SET status = 'delivered', claimed_at = NULL, updated_at = $4
        WHERE tenant_id = $1 AND job_id = $2 AND event_id = $3`,
		report.TenantID, report.JobID, report.EventID, at.UTC())
	return err
}

func (r *PostgresInstructionOutcomeRepository) Retry(
	ctx context.Context,
	report application.DriverInstructionReport,
	attempts int,
	next time.Time,
	lastError string,
	abandon bool,
	at time.Time,
) error {
	status := "pending"
	if abandon {
		status = "abandoned"
	}
	_, err := r.db.ExecContext(ctx, `
        UPDATE job_instruction_outcomes
        SET status = $4, attempts = $5, next_attempt_at = $6, last_error = $7, claimed_at = NULL, updated_at = $8
        WHERE tenant_id = $1 AND job_id = $2 AND event_id = $3`,
		report.TenantID, report.JobID, report.EventID, status, attempts, next.UTC(), lastError, at.UTC())
	return err
}

// ClaimDue spans tenants because the sweep is the platform's own; every row
// carries its tenant and each later write is keyed by tenant, job and event.
func (r *PostgresInstructionOutcomeRepository) ClaimDue(ctx context.Context, now, claimedBefore time.Time, limit int) ([]application.PendingInstructionOutcome, error) {
	rows, err := r.db.QueryContext(ctx, `
        UPDATE job_instruction_outcomes SET claimed_at = $1, updated_at = $1
        WHERE (tenant_id, job_id, event_id) IN (
            SELECT tenant_id, job_id, event_id FROM job_instruction_outcomes
            WHERE status = 'pending' AND next_attempt_at <= $1 AND (claimed_at IS NULL OR claimed_at < $2)
            ORDER BY next_attempt_at ASC LIMIT $3
            FOR UPDATE SKIP LOCKED)
        RETURNING tenant_id, job_id, event_id, driver_id, outcome, channel, reason, attempts`,
		now.UTC(), claimedBefore.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	claimed := []application.PendingInstructionOutcome{}
	for rows.Next() {
		var pending application.PendingInstructionOutcome
		report := &pending.Report
		if err := rows.Scan(&report.TenantID, &report.JobID, &report.EventID, &report.DriverID, &report.Outcome, &report.Channel, &report.Reason, &pending.Attempts); err != nil {
			return nil, err
		}
		claimed = append(claimed, pending)
	}
	return claimed, rows.Err()
}
