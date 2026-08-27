package infrastructure

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const aggregatorActivitySchema = `
CREATE TABLE IF NOT EXISTS aggregator_activity (
    id TEXT PRIMARY KEY,
    aggregator_tenant_id TEXT NOT NULL,
    type TEXT NOT NULL,
    description TEXT NOT NULL,
    related_entity_type TEXT NOT NULL DEFAULT '',
    related_entity_id TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_aggregator_activity_aggregator ON aggregator_activity (aggregator_tenant_id, occurred_at DESC);
`

type PostgresAggregatorActivityRepository struct {
	db *sql.DB
}

func NewPostgresAggregatorActivityRepository(db *sql.DB) *PostgresAggregatorActivityRepository {
	return &PostgresAggregatorActivityRepository{db: db}
}

func (r *PostgresAggregatorActivityRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, aggregatorActivitySchema)
	return err
}

func (r *PostgresAggregatorActivityRepository) Append(ctx context.Context, entry domain.AggregatorActivityEntry) error {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO aggregator_activity
            (id, aggregator_tenant_id, type, description, related_entity_type, related_entity_id, occurred_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		entry.ID, entry.AggregatorTenantID, string(entry.Type), entry.Description,
		entry.RelatedEntityType, entry.RelatedEntityID, entry.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("insert aggregator activity entry: %w", err)
	}
	return nil
}

func (r *PostgresAggregatorActivityRepository) ListByAggregator(ctx context.Context, aggregatorTenantID string, limit int) ([]domain.AggregatorActivityEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, aggregator_tenant_id, type, description, related_entity_type, related_entity_id, occurred_at
         FROM aggregator_activity WHERE aggregator_tenant_id = $1 ORDER BY occurred_at DESC LIMIT $2`,
		aggregatorTenantID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []domain.AggregatorActivityEntry{}
	for rows.Next() {
		var entry domain.AggregatorActivityEntry
		var activityType string
		if err := rows.Scan(&entry.ID, &entry.AggregatorTenantID, &activityType, &entry.Description,
			&entry.RelatedEntityType, &entry.RelatedEntityID, &entry.OccurredAt); err != nil {
			return nil, err
		}
		entry.Type = domain.AggregatorActivityType(activityType)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
