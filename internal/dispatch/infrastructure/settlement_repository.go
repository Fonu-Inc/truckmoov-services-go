package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const settlementSchema = `
CREATE TABLE IF NOT EXISTS aggregator_settlements (
    id TEXT PRIMARY KEY,
    aggregator_tenant_id TEXT NOT NULL,
    operator_tenant_id TEXT NOT NULL,
    relationship_id TEXT NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    gross_earnings_ngn NUMERIC NOT NULL,
    fee_rate_pct_applied NUMERIC NOT NULL,
    aggregator_fee_ngn NUMERIC NOT NULL,
    net_payout_ngn NUMERIC NOT NULL,
    status TEXT NOT NULL,
    payment_reference TEXT NOT NULL DEFAULT '',
    paid_at TIMESTAMPTZ NULL,
    paid_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (aggregator_tenant_id, operator_tenant_id, period_start)
);
CREATE INDEX IF NOT EXISTS idx_aggregator_settlements_aggregator ON aggregator_settlements (aggregator_tenant_id, created_at DESC);
`

type PostgresSettlementRepository struct {
	db *sql.DB
}

func NewPostgresSettlementRepository(db *sql.DB) *PostgresSettlementRepository {
	return &PostgresSettlementRepository{db: db}
}

func (r *PostgresSettlementRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, settlementSchema)
	return err
}

const settlementColumns = `id, aggregator_tenant_id, operator_tenant_id, relationship_id, period_start, period_end,
    gross_earnings_ngn, fee_rate_pct_applied, aggregator_fee_ngn, net_payout_ngn, status,
    payment_reference, paid_at, paid_by, created_at, updated_at`

func scanSettlement(scanner interface{ Scan(...any) error }) (domain.Settlement, error) {
	var s domain.Settlement
	var status string
	err := scanner.Scan(
		&s.ID, &s.AggregatorTenantID, &s.OperatorTenantID, &s.RelationshipID, &s.PeriodStart, &s.PeriodEnd,
		&s.GrossEarningsNgn, &s.FeeRatePctApplied, &s.AggregatorFeeNgn, &s.NetPayoutNgn, &status,
		&s.PaymentReference, &s.PaidAt, &s.PaidBy, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return domain.Settlement{}, err
	}
	s.Status = domain.SettlementStatus(status)
	return s, nil
}

func (r *PostgresSettlementRepository) Create(ctx context.Context, settlement domain.Settlement) (domain.Settlement, error) {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO aggregator_settlements
            (`+settlementColumns+`)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		settlement.ID, settlement.AggregatorTenantID, settlement.OperatorTenantID, settlement.RelationshipID,
		settlement.PeriodStart, settlement.PeriodEnd, settlement.GrossEarningsNgn, settlement.FeeRatePctApplied,
		settlement.AggregatorFeeNgn, settlement.NetPayoutNgn, string(settlement.Status), settlement.PaymentReference,
		settlement.PaidAt, settlement.PaidBy, settlement.CreatedAt, settlement.UpdatedAt,
	)
	if err != nil {
		return domain.Settlement{}, fmt.Errorf("insert settlement: %w", err)
	}
	return settlement, nil
}

func (r *PostgresSettlementRepository) FindByID(ctx context.Context, id string) (domain.Settlement, bool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+settlementColumns+` FROM aggregator_settlements WHERE id = $1`, id)
	s, err := scanSettlement(row)
	if err == sql.ErrNoRows {
		return domain.Settlement{}, false, nil
	}
	if err != nil {
		return domain.Settlement{}, false, err
	}
	return s, true, nil
}

func (r *PostgresSettlementRepository) FindForPeriod(ctx context.Context, aggregatorTenantID, operatorTenantID string, periodStart time.Time) (domain.Settlement, bool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+settlementColumns+` FROM aggregator_settlements
         WHERE aggregator_tenant_id = $1 AND operator_tenant_id = $2 AND period_start = $3`,
		aggregatorTenantID, operatorTenantID, periodStart,
	)
	s, err := scanSettlement(row)
	if err == sql.ErrNoRows {
		return domain.Settlement{}, false, nil
	}
	if err != nil {
		return domain.Settlement{}, false, err
	}
	return s, true, nil
}

func (r *PostgresSettlementRepository) ListByAggregator(ctx context.Context, aggregatorTenantID string, limit, offset int) ([]domain.Settlement, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM aggregator_settlements WHERE aggregator_tenant_id = $1`, aggregatorTenantID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+settlementColumns+` FROM aggregator_settlements
         WHERE aggregator_tenant_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		aggregatorTenantID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	settlements := []domain.Settlement{}
	for rows.Next() {
		s, err := scanSettlement(rows)
		if err != nil {
			return nil, 0, err
		}
		settlements = append(settlements, s)
	}
	return settlements, total, rows.Err()
}

func (r *PostgresSettlementRepository) Save(ctx context.Context, settlement domain.Settlement) error {
	_, err := r.db.ExecContext(ctx, `
        UPDATE aggregator_settlements SET
            status = $1, payment_reference = $2, paid_at = $3, paid_by = $4, updated_at = $5
        WHERE id = $6`,
		string(settlement.Status), settlement.PaymentReference, settlement.PaidAt, settlement.PaidBy,
		settlement.UpdatedAt, settlement.ID,
	)
	return err
}
