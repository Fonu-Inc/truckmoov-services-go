package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const jobOfferSchema = `
CREATE TABLE IF NOT EXISTS job_offers (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    offering_tenant_id TEXT NOT NULL,
    target_tenant_id TEXT NOT NULL,
    offered_rate_ngn NUMERIC NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    expires_at TIMESTAMPTZ NOT NULL,
    response_note TEXT NOT NULL DEFAULT '',
    responded_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_job_offers_tenant_job ON job_offers (offering_tenant_id, job_id);
CREATE INDEX IF NOT EXISTS idx_job_offers_target_status ON job_offers (target_tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_job_offers_offering_status ON job_offers (offering_tenant_id, status);
-- Read by the expiry sweep: pending offers ordered by how soon they expire.
CREATE INDEX IF NOT EXISTS idx_job_offers_pending_expiry
    ON job_offers (expires_at)
    WHERE status = 'pending';
`

// PostgresJobOfferRepository owns both job_offers and, for the Accept path
// only, direct writes to dispatch_jobs.
//
// That second part is deliberate and narrow: accepting an offer has to
// withdraw sibling offers and transfer the job in one transaction, and the
// existing JobRepository interface has no transaction hook — its Save is an
// independent upsert keyed on (tenant_id, id), which cannot even express
// moving a job's tenant_id without knowing the schema. Every other operation
// here goes through domain.JobOffer and touches only job_offers.
type PostgresJobOfferRepository struct {
	db *sql.DB
}

func NewPostgresJobOfferRepository(db *sql.DB) *PostgresJobOfferRepository {
	return &PostgresJobOfferRepository{db: db}
}

func (r *PostgresJobOfferRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, jobOfferSchema)
	return err
}

func (r *PostgresJobOfferRepository) Create(ctx context.Context, offer domain.JobOffer) (domain.JobOffer, error) {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO job_offers
            (id, job_id, offering_tenant_id, target_tenant_id, offered_rate_ngn,
             status, expires_at, response_note, responded_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		offer.ID, offer.JobID, offer.OfferingTenantID, offer.TargetTenantID,
		offer.OfferedRateNgn, offer.Status, offer.ExpiresAt, offer.ResponseNote,
		offer.RespondedAt, offer.CreatedAt, offer.UpdatedAt,
	)
	if err != nil {
		return domain.JobOffer{}, fmt.Errorf("insert job offer: %w", err)
	}
	return offer, nil
}

const jobOfferColumns = `id, job_id, offering_tenant_id, target_tenant_id, offered_rate_ngn,
    status, expires_at, response_note, responded_at, created_at, updated_at`

func scanJobOffer(scanner interface{ Scan(...any) error }) (domain.JobOffer, error) {
	var offer domain.JobOffer
	err := scanner.Scan(
		&offer.ID, &offer.JobID, &offer.OfferingTenantID, &offer.TargetTenantID,
		&offer.OfferedRateNgn, &offer.Status, &offer.ExpiresAt, &offer.ResponseNote,
		&offer.RespondedAt, &offer.CreatedAt, &offer.UpdatedAt,
	)
	return offer, err
}

func (r *PostgresJobOfferRepository) FindByID(ctx context.Context, offerID string) (domain.JobOffer, bool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+jobOfferColumns+` FROM job_offers WHERE id = $1`, offerID)
	offer, err := scanJobOffer(row)
	if err == sql.ErrNoRows {
		return domain.JobOffer{}, false, nil
	}
	if err != nil {
		return domain.JobOffer{}, false, err
	}
	return offer, true, nil
}

func (r *PostgresJobOfferRepository) List(ctx context.Context, query application.JobOfferQuery) ([]domain.JobOffer, int, error) {
	tenantColumn := "target_tenant_id"
	if query.Role == "offering" {
		tenantColumn = "offering_tenant_id"
	}

	where := []string{tenantColumn + " = $1"}
	args := []any{query.TenantID}
	if strings.TrimSpace(query.Status) != "" {
		args = append(args, query.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_offers WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit, offset := jobOfferPageBounds(query.Page, query.Limit)
	args = append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobOfferColumns+` FROM job_offers WHERE `+clause+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	offers := []domain.JobOffer{}
	for rows.Next() {
		offer, err := scanJobOffer(rows)
		if err != nil {
			return nil, 0, err
		}
		offers = append(offers, offer)
	}
	return offers, total, rows.Err()
}

func jobOfferPageBounds(page, limit int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if page <= 0 {
		page = 1
	}
	return limit, (page - 1) * limit
}

func (r *PostgresJobOfferRepository) CountPending(ctx context.Context, targetTenantID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM job_offers WHERE target_tenant_id = $1 AND status = $2`,
		targetTenantID, domain.OfferStatusPending,
	).Scan(&count)
	return count, err
}

func (r *PostgresJobOfferRepository) ListAccepted(ctx context.Context, offeringTenantID, targetTenantID string) ([]domain.JobOffer, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobOfferColumns+` FROM job_offers
         WHERE offering_tenant_id = $1 AND target_tenant_id = $2 AND status = $3
         ORDER BY responded_at DESC`,
		offeringTenantID, targetTenantID, domain.OfferStatusAccepted,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offers := []domain.JobOffer{}
	for rows.Next() {
		offer, err := scanJobOffer(rows)
		if err != nil {
			return nil, err
		}
		offers = append(offers, offer)
	}
	return offers, rows.Err()
}

func (r *PostgresJobOfferRepository) FindExpiring(ctx context.Context, before time.Time, limit int) ([]domain.JobOffer, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobOfferColumns+` FROM job_offers
         WHERE status = $1 AND expires_at <= $2
         ORDER BY expires_at ASC LIMIT $3`,
		domain.OfferStatusPending, before, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offers := []domain.JobOffer{}
	for rows.Next() {
		offer, err := scanJobOffer(rows)
		if err != nil {
			return nil, err
		}
		offers = append(offers, offer)
	}
	return offers, rows.Err()
}

func (r *PostgresJobOfferRepository) SaveExpired(ctx context.Context, offer domain.JobOffer) error {
	return r.Save(ctx, offer)
}

func (r *PostgresJobOfferRepository) Save(ctx context.Context, offer domain.JobOffer) error {
	_, err := r.db.ExecContext(ctx, `
        UPDATE job_offers SET
            status = $1, response_note = $2, responded_at = $3, updated_at = $4
        WHERE id = $5 AND offering_tenant_id = $6 AND target_tenant_id = $7`,
		offer.Status, offer.ResponseNote, offer.RespondedAt, offer.UpdatedAt, offer.ID,
		offer.OfferingTenantID, offer.TargetTenantID,
	)
	return err
}

// Accept is the transactional heart of this screen. Everything the
// requirements ask for happens inside one transaction, re-validated under a
// row lock so a race between two carriers resolves correctly rather than by
// whichever request's earlier read happened to be newer:
//
//  1. Lock and re-check the target offer (pending, not expired, belongs to
//     this tenant) — a second acceptance racing in cannot slip past an earlier
//     read.
//  2. Withdraw every other pending offer for the same job, so a job offered to
//     several carriers cannot be believed to belong to two of them.
//  3. Lock and re-check the job (not cancelled/delivered), then move it from
//     its (offering_tenant_id, id) row to (target_tenant_id, id) — a delete and
//     insert, because tenant_id is part of the primary key and cannot be
//     updated as a normal column.
func (r *PostgresJobOfferRepository) Accept(
	ctx context.Context,
	offerID, targetTenantID, note string,
	now time.Time,
) (application.AcceptedOffer, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("begin accept transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var offer domain.JobOffer
	row := tx.QueryRowContext(ctx, `SELECT `+jobOfferColumns+` FROM job_offers WHERE id = $1 FOR UPDATE`, offerID)
	offer, err = scanJobOffer(row)
	if err == sql.ErrNoRows {
		return application.AcceptedOffer{}, application.ErrJobOfferNotFound
	}
	if err != nil {
		return application.AcceptedOffer{}, err
	}
	if offer.TargetTenantID != targetTenantID {
		return application.AcceptedOffer{}, application.ErrNotYourOffer
	}

	accepted, err := offer.Accept(note, now)
	if err != nil {
		return application.AcceptedOffer{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE job_offers SET status=$1, response_note=$2, responded_at=$3, updated_at=$4 WHERE id=$5`,
		accepted.Status, accepted.ResponseNote, accepted.RespondedAt, accepted.UpdatedAt, accepted.ID,
	); err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("save accepted offer: %w", err)
	}

	siblingRows, err := tx.QueryContext(ctx,
		`SELECT `+jobOfferColumns+` FROM job_offers WHERE offering_tenant_id = $1 AND job_id = $2 AND id <> $3 AND status = $4 FOR UPDATE`,
		offer.OfferingTenantID, offer.JobID, offer.ID, domain.OfferStatusPending,
	)
	if err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("load sibling offers: %w", err)
	}
	siblings := []domain.JobOffer{}
	for siblingRows.Next() {
		sibling, err := scanJobOffer(siblingRows)
		if err != nil {
			siblingRows.Close()
			return application.AcceptedOffer{}, err
		}
		siblings = append(siblings, sibling)
	}
	siblingRows.Close()
	if err := siblingRows.Err(); err != nil {
		return application.AcceptedOffer{}, err
	}
	for _, sibling := range siblings {
		withdrawn := sibling.Withdraw(now)
		if _, err := tx.ExecContext(ctx,
			`UPDATE job_offers SET status=$1, response_note=$2, responded_at=$3, updated_at=$4 WHERE id=$5`,
			withdrawn.Status, withdrawn.ResponseNote, withdrawn.RespondedAt, withdrawn.UpdatedAt, withdrawn.ID,
		); err != nil {
			return application.AcceptedOffer{}, fmt.Errorf("withdraw sibling offer: %w", err)
		}
	}

	var payload []byte
	var deadline sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT payload, deadline FROM dispatch_jobs WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
		offer.OfferingTenantID, offer.JobID,
	).Scan(&payload, &deadline)
	if err == sql.ErrNoRows {
		return application.AcceptedOffer{}, application.ErrJobNotFound
	}
	if err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("load job for transfer: %w", err)
	}

	var job domain.Job
	if err := json.Unmarshal(payload, &job); err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("decode job payload: %w", err)
	}

	transferred, err := job.TransferToCarrier(targetTenantID, offer.OfferedRateNgn, now, "timeline-"+accepted.ID)
	if err != nil {
		return application.AcceptedOffer{}, err
	}

	newPayload, err := json.Marshal(transferred)
	if err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("encode transferred job: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM dispatch_jobs WHERE tenant_id = $1 AND id = $2`,
		offer.OfferingTenantID, offer.JobID,
	); err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("remove job from offering tenant: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO dispatch_jobs (
            tenant_id, id, reference, status, priority, pickup_address, dropoff_address,
            cargo_type, cargo_description, assigned_driver_id, assigned_driver_phone,
            assigned_vehicle_id, created_by, deadline, delivered_at, created_at, updated_at, payload
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		transferred.TenantID, transferred.ID, transferred.Reference, transferred.Status,
		transferred.Priority, transferred.Pickup.Address, transferred.Dropoff.Address,
		transferred.CargoType, transferred.CargoDescription, transferred.AssignedDriverID,
		transferred.AssignedDriverPhone, transferred.AssignedVehicleID, transferred.CreatedBy, deadline, nil,
		transferred.CreatedAt, transferred.UpdatedAt, newPayload,
	); err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("insert job under carrier tenant: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE dispatch_job_attachments SET tenant_id = $1 WHERE tenant_id = $2 AND job_id = $3`,
		transferred.TenantID, offer.OfferingTenantID, offer.JobID,
	); err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("move job attachments to carrier tenant: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return application.AcceptedOffer{}, fmt.Errorf("commit accept transaction: %w", err)
	}

	return application.AcceptedOffer{Offer: accepted, Job: transferred}, nil
}
