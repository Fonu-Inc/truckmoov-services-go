package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const aggregatorRelationshipSchema = `
CREATE TABLE IF NOT EXISTS aggregator_relationships (
    id TEXT PRIMARY KEY,
    aggregator_tenant_id TEXT NOT NULL,
    operator_tenant_id TEXT NOT NULL DEFAULT '',
    invited_email TEXT NOT NULL DEFAULT '',
    types JSONB NOT NULL DEFAULT '[]',
    fee_rate_pct NUMERIC NOT NULL DEFAULT 0,
    regions JSONB NOT NULL DEFAULT '[]',
    status TEXT NOT NULL,
    ended_at TIMESTAMPTZ NULL,
    ended_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_aggregator_relationships_aggregator ON aggregator_relationships (aggregator_tenant_id);
CREATE INDEX IF NOT EXISTS idx_aggregator_relationships_pair ON aggregator_relationships (aggregator_tenant_id, operator_tenant_id);
`

type PostgresAggregatorRelationshipRepository struct {
	db *sql.DB
}

func NewPostgresAggregatorRelationshipRepository(db *sql.DB) *PostgresAggregatorRelationshipRepository {
	return &PostgresAggregatorRelationshipRepository{db: db}
}

func (r *PostgresAggregatorRelationshipRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, aggregatorRelationshipSchema)
	return err
}

const aggregatorRelationshipColumns = `id, aggregator_tenant_id, operator_tenant_id, invited_email, types,
    fee_rate_pct, regions, status, ended_at, ended_reason, created_at, updated_at`

func scanAggregatorRelationship(scanner interface{ Scan(...any) error }) (domain.AggregatorRelationship, error) {
	var rel domain.AggregatorRelationship
	var typesJSON, regionsJSON []byte
	var status string
	err := scanner.Scan(
		&rel.ID, &rel.AggregatorTenantID, &rel.OperatorTenantID, &rel.InvitedEmail, &typesJSON,
		&rel.FeeRatePct, &regionsJSON, &status, &rel.EndedAt, &rel.EndedReason, &rel.CreatedAt, &rel.UpdatedAt,
	)
	if err != nil {
		return domain.AggregatorRelationship{}, err
	}
	rel.Status = domain.RelationshipStatus(status)

	var types []string
	if err := json.Unmarshal(typesJSON, &types); err != nil {
		return domain.AggregatorRelationship{}, fmt.Errorf("decode relationship types: %w", err)
	}
	for _, t := range types {
		rel.Types = append(rel.Types, domain.RelationshipType(t))
	}

	var regions []string
	if err := json.Unmarshal(regionsJSON, &regions); err != nil {
		return domain.AggregatorRelationship{}, fmt.Errorf("decode relationship regions: %w", err)
	}
	for _, reg := range regions {
		rel.Regions = append(rel.Regions, domain.Region(reg))
	}

	return rel, nil
}

func (r *PostgresAggregatorRelationshipRepository) Create(ctx context.Context, relationship domain.AggregatorRelationship) (domain.AggregatorRelationship, error) {
	typesJSON, regionsJSON, err := marshalRelationshipLists(relationship)
	if err != nil {
		return domain.AggregatorRelationship{}, err
	}
	_, err = r.db.ExecContext(ctx, `
        INSERT INTO aggregator_relationships
            (id, aggregator_tenant_id, operator_tenant_id, invited_email, types,
             fee_rate_pct, regions, status, ended_at, ended_reason, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		relationship.ID, relationship.AggregatorTenantID, relationship.OperatorTenantID, relationship.InvitedEmail, typesJSON,
		relationship.FeeRatePct, regionsJSON, string(relationship.Status), relationship.EndedAt, relationship.EndedReason,
		relationship.CreatedAt, relationship.UpdatedAt,
	)
	if err != nil {
		return domain.AggregatorRelationship{}, fmt.Errorf("insert aggregator relationship: %w", err)
	}
	return relationship, nil
}

func (r *PostgresAggregatorRelationshipRepository) FindByID(ctx context.Context, id string) (domain.AggregatorRelationship, bool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+aggregatorRelationshipColumns+` FROM aggregator_relationships WHERE id = $1`, id)
	rel, err := scanAggregatorRelationship(row)
	if err == sql.ErrNoRows {
		return domain.AggregatorRelationship{}, false, nil
	}
	if err != nil {
		return domain.AggregatorRelationship{}, false, err
	}
	return rel, true, nil
}

func (r *PostgresAggregatorRelationshipRepository) FindActiveBetween(ctx context.Context, aggregatorTenantID, operatorTenantID string) (domain.AggregatorRelationship, bool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+aggregatorRelationshipColumns+` FROM aggregator_relationships
         WHERE aggregator_tenant_id = $1 AND operator_tenant_id = $2 AND status <> 'ended'
         ORDER BY created_at DESC LIMIT 1`,
		aggregatorTenantID, operatorTenantID,
	)
	rel, err := scanAggregatorRelationship(row)
	if err == sql.ErrNoRows {
		return domain.AggregatorRelationship{}, false, nil
	}
	if err != nil {
		return domain.AggregatorRelationship{}, false, err
	}
	return rel, true, nil
}

func (r *PostgresAggregatorRelationshipRepository) ListByAggregator(ctx context.Context, aggregatorTenantID string) ([]domain.AggregatorRelationship, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+aggregatorRelationshipColumns+` FROM aggregator_relationships WHERE aggregator_tenant_id = $1 ORDER BY created_at DESC`,
		aggregatorTenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	relationships := []domain.AggregatorRelationship{}
	for rows.Next() {
		rel, err := scanAggregatorRelationship(rows)
		if err != nil {
			return nil, err
		}
		relationships = append(relationships, rel)
	}
	return relationships, rows.Err()
}

func (r *PostgresAggregatorRelationshipRepository) ListDistinctAggregatorTenants(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT aggregator_tenant_id FROM aggregator_relationships`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tenants := []string{}
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenantID)
	}
	return tenants, rows.Err()
}

func (r *PostgresAggregatorRelationshipRepository) Save(ctx context.Context, relationship domain.AggregatorRelationship) error {
	typesJSON, regionsJSON, err := marshalRelationshipLists(relationship)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
        UPDATE aggregator_relationships SET
            operator_tenant_id = $1, invited_email = $2, types = $3, fee_rate_pct = $4,
            regions = $5, status = $6, ended_at = $7, ended_reason = $8, updated_at = $9
        WHERE id = $10`,
		relationship.OperatorTenantID, relationship.InvitedEmail, typesJSON, relationship.FeeRatePct,
		regionsJSON, string(relationship.Status), relationship.EndedAt, relationship.EndedReason,
		relationship.UpdatedAt, relationship.ID,
	)
	return err
}

func marshalRelationshipLists(relationship domain.AggregatorRelationship) ([]byte, []byte, error) {
	types := make([]string, 0, len(relationship.Types))
	for _, t := range relationship.Types {
		types = append(types, string(t))
	}
	typesJSON, err := json.Marshal(types)
	if err != nil {
		return nil, nil, fmt.Errorf("encode relationship types: %w", err)
	}

	regions := make([]string, 0, len(relationship.Regions))
	for _, reg := range relationship.Regions {
		regions = append(regions, string(reg))
	}
	regionsJSON, err := json.Marshal(regions)
	if err != nil {
		return nil, nil, fmt.Errorf("encode relationship regions: %w", err)
	}

	return typesJSON, regionsJSON, nil
}
