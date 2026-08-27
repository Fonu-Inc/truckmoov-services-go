package infrastructure

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const operatorRatingSchema = `
CREATE TABLE IF NOT EXISTS operator_ratings (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL UNIQUE,
    rater_tenant_id TEXT NOT NULL,
    operator_tenant_id TEXT NOT NULL,
    value INT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_operator_ratings_operator ON operator_ratings (operator_tenant_id);
`

type PostgresOperatorRatingRepository struct {
	db *sql.DB
}

func NewPostgresOperatorRatingRepository(db *sql.DB) *PostgresOperatorRatingRepository {
	return &PostgresOperatorRatingRepository{db: db}
}

func (r *PostgresOperatorRatingRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, operatorRatingSchema)
	return err
}

func (r *PostgresOperatorRatingRepository) Create(ctx context.Context, rating domain.OperatorRating) (domain.OperatorRating, error) {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO operator_ratings (id, job_id, rater_tenant_id, operator_tenant_id, value, note, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		rating.ID, rating.JobID, rating.RaterTenantID, rating.OperatorTenantID, rating.Value, rating.Note, rating.CreatedAt,
	)
	if err != nil {
		return domain.OperatorRating{}, fmt.Errorf("insert operator rating: %w", err)
	}
	return rating, nil
}

func (r *PostgresOperatorRatingRepository) FindByJob(ctx context.Context, jobID string) (domain.OperatorRating, bool, error) {
	var rating domain.OperatorRating
	err := r.db.QueryRowContext(ctx,
		`SELECT id, job_id, rater_tenant_id, operator_tenant_id, value, note, created_at
         FROM operator_ratings WHERE job_id = $1`, jobID,
	).Scan(&rating.ID, &rating.JobID, &rating.RaterTenantID, &rating.OperatorTenantID, &rating.Value, &rating.Note, &rating.CreatedAt)
	if err == sql.ErrNoRows {
		return domain.OperatorRating{}, false, nil
	}
	if err != nil {
		return domain.OperatorRating{}, false, err
	}
	return rating, true, nil
}

func (r *PostgresOperatorRatingRepository) AverageForOperator(ctx context.Context, operatorTenantID string) (float64, int, error) {
	var avg sql.NullFloat64
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT AVG(value), COUNT(*) FROM operator_ratings WHERE operator_tenant_id = $1`, operatorTenantID,
	).Scan(&avg, &count)
	if err != nil {
		return 0, 0, err
	}
	if !avg.Valid {
		return 0, 0, nil
	}
	return roundToTwoDecimalsInfra(avg.Float64), count, nil
}

func roundToTwoDecimalsInfra(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}
