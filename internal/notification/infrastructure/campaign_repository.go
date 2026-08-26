package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

const campaignSchema = `
CREATE TABLE IF NOT EXISTS campaigns (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    channels JSONB NOT NULL DEFAULT '[]',
    bodies JSONB NOT NULL DEFAULT '{}',
    template_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft',
    scheduled_for TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_campaigns_tenant_status ON campaigns (tenant_id, status);

CREATE TABLE IF NOT EXISTS campaign_recipients (
    id TEXT PRIMARY KEY,
    campaign_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    driver_id TEXT NOT NULL DEFAULT '',
    channel TEXT NOT NULL,
    destination TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'queued',
    provider_message_id TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    sent_at TIMESTAMPTZ NULL,
    delivered_at TIMESTAMPTZ NULL,
    read_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_campaign_recipients_campaign ON campaign_recipients (tenant_id, campaign_id);
-- Partial: most rows carry no provider id, and this index exists only to match
-- an incoming receipt back to its recipient.
CREATE UNIQUE INDEX IF NOT EXISTS idx_campaign_recipients_provider_message
    ON campaign_recipients (provider_message_id)
    WHERE provider_message_id <> '';
`

type PostgresCampaignRepository struct {
	db *sql.DB
}

func NewPostgresCampaignRepository(db *sql.DB) *PostgresCampaignRepository {
	return &PostgresCampaignRepository{db: db}
}

func (r *PostgresCampaignRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, campaignSchema)
	return err
}

func (r *PostgresCampaignRepository) SaveCampaign(ctx context.Context, campaign domain.Campaign) (domain.Campaign, error) {
	channels, err := json.Marshal(campaign.Channels)
	if err != nil {
		return domain.Campaign{}, err
	}
	bodies, err := json.Marshal(campaign.Bodies)
	if err != nil {
		return domain.Campaign{}, err
	}

	_, err = r.db.ExecContext(ctx, `
        INSERT INTO campaigns (id, tenant_id, name, channels, bodies, template_name, status, scheduled_for, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
        ON CONFLICT (id) DO UPDATE SET
            name = EXCLUDED.name,
            channels = EXCLUDED.channels,
            bodies = EXCLUDED.bodies,
            template_name = EXCLUDED.template_name,
            status = EXCLUDED.status,
            scheduled_for = EXCLUDED.scheduled_for,
            updated_at = EXCLUDED.updated_at`,
		campaign.ID, campaign.TenantID, campaign.Name, channels, bodies,
		campaign.TemplateName, campaign.Status, campaign.ScheduledFor,
		campaign.CreatedAt, campaign.UpdatedAt,
	)
	if err != nil {
		return domain.Campaign{}, fmt.Errorf("save campaign: %w", err)
	}
	return campaign, nil
}

func scanCampaign(scanner interface{ Scan(...any) error }) (domain.Campaign, error) {
	var campaign domain.Campaign
	var channels, bodies []byte
	if err := scanner.Scan(
		&campaign.ID, &campaign.TenantID, &campaign.Name, &channels, &bodies,
		&campaign.TemplateName, &campaign.Status, &campaign.ScheduledFor,
		&campaign.CreatedAt, &campaign.UpdatedAt,
	); err != nil {
		return domain.Campaign{}, err
	}
	if err := json.Unmarshal(channels, &campaign.Channels); err != nil {
		return domain.Campaign{}, err
	}
	if err := json.Unmarshal(bodies, &campaign.Bodies); err != nil {
		return domain.Campaign{}, err
	}
	return campaign, nil
}

const campaignColumns = `id, tenant_id, name, channels, bodies, template_name, status, scheduled_for, created_at, updated_at`

func (r *PostgresCampaignRepository) FindCampaign(ctx context.Context, tenantID, campaignID string) (domain.Campaign, bool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+campaignColumns+` FROM campaigns WHERE tenant_id = $1 AND id = $2`,
		tenantID, campaignID,
	)
	campaign, err := scanCampaign(row)
	if err == sql.ErrNoRows {
		return domain.Campaign{}, false, nil
	}
	if err != nil {
		return domain.Campaign{}, false, err
	}
	return campaign, true, nil
}

func (r *PostgresCampaignRepository) ListCampaigns(
	ctx context.Context,
	query application.ListCampaignsQuery,
) ([]domain.Campaign, int, error) {
	where := []string{"tenant_id = $1"}
	args := []any{query.TenantID}

	if strings.TrimSpace(query.Status) != "" {
		args = append(args, query.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if search := strings.TrimSpace(query.Query); search != "" {
		args = append(args, "%"+search+"%")
		// Name and body both, because the list search names both.
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR bodies::text ILIKE $%d)", len(args), len(args)))
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM campaigns WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit, offset := pageBounds(query.Page, query.Limit)
	args = append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+campaignColumns+` FROM campaigns WHERE `+clause+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	campaigns := []domain.Campaign{}
	for rows.Next() {
		campaign, err := scanCampaign(rows)
		if err != nil {
			return nil, 0, err
		}
		campaigns = append(campaigns, campaign)
	}
	return campaigns, total, rows.Err()
}

func pageBounds(page, limit int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if page <= 0 {
		page = 1
	}
	return limit, (page - 1) * limit
}

func (r *PostgresCampaignRepository) SaveRecipients(ctx context.Context, recipients []domain.CampaignRecipient) error {
	for _, recipient := range recipients {
		if err := r.SaveRecipient(ctx, recipient); err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresCampaignRepository) SaveRecipient(ctx context.Context, recipient domain.CampaignRecipient) error {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO campaign_recipients
            (id, campaign_id, tenant_id, driver_id, channel, destination, state,
             provider_message_id, failure_reason, sent_at, delivered_at, read_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
        ON CONFLICT (id) DO UPDATE SET
            state = EXCLUDED.state,
            provider_message_id = EXCLUDED.provider_message_id,
            failure_reason = EXCLUDED.failure_reason,
            sent_at = EXCLUDED.sent_at,
            delivered_at = EXCLUDED.delivered_at,
            read_at = EXCLUDED.read_at,
            updated_at = EXCLUDED.updated_at`,
		recipient.ID, recipient.CampaignID, recipient.TenantID, recipient.DriverID,
		recipient.Channel, recipient.Destination, recipient.State,
		recipient.ProviderMessageID, recipient.FailureReason,
		recipient.SentAt, recipient.DeliveredAt, recipient.ReadAt,
		recipient.CreatedAt, recipient.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("save campaign recipient: %w", err)
	}
	return nil
}

const recipientColumns = `id, campaign_id, tenant_id, driver_id, channel, destination, state,
    provider_message_id, failure_reason, sent_at, delivered_at, read_at, created_at, updated_at`

func scanRecipient(scanner interface{ Scan(...any) error }) (domain.CampaignRecipient, error) {
	var recipient domain.CampaignRecipient
	err := scanner.Scan(
		&recipient.ID, &recipient.CampaignID, &recipient.TenantID, &recipient.DriverID,
		&recipient.Channel, &recipient.Destination, &recipient.State,
		&recipient.ProviderMessageID, &recipient.FailureReason,
		&recipient.SentAt, &recipient.DeliveredAt, &recipient.ReadAt,
		&recipient.CreatedAt, &recipient.UpdatedAt,
	)
	return recipient, err
}

func (r *PostgresCampaignRepository) ListRecipients(ctx context.Context, tenantID, campaignID string) ([]domain.CampaignRecipient, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+recipientColumns+` FROM campaign_recipients
         WHERE tenant_id = $1 AND campaign_id = $2 ORDER BY created_at ASC`,
		tenantID, campaignID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	recipients := []domain.CampaignRecipient{}
	for rows.Next() {
		recipient, err := scanRecipient(rows)
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, recipient)
	}
	return recipients, rows.Err()
}

func (r *PostgresCampaignRepository) FindRecipientByProviderMessageID(
	ctx context.Context,
	providerMessageID string,
) (domain.CampaignRecipient, bool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+recipientColumns+` FROM campaign_recipients WHERE provider_message_id = $1`,
		providerMessageID,
	)
	recipient, err := scanRecipient(row)
	if err == sql.ErrNoRows {
		return domain.CampaignRecipient{}, false, nil
	}
	if err != nil {
		return domain.CampaignRecipient{}, false, err
	}
	return recipient, true, nil
}

func (r *PostgresCampaignRepository) SummarizeAll(ctx context.Context, tenantID string) (domain.CampaignProgress, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT state, channel, COUNT(*) FROM campaign_recipients WHERE tenant_id = $1 GROUP BY state, channel`,
		tenantID,
	)
	if err != nil {
		return domain.CampaignProgress{}, err
	}
	defer rows.Close()

	// Counted in the database and folded through the same summariser the
	// per-campaign figures use, so the two can never disagree.
	synthetic := []domain.CampaignRecipient{}
	for rows.Next() {
		var state, channel string
		var count int
		if err := rows.Scan(&state, &channel, &count); err != nil {
			return domain.CampaignProgress{}, err
		}
		for index := 0; index < count; index++ {
			synthetic = append(synthetic, domain.CampaignRecipient{State: state, Channel: channel})
		}
	}
	if err := rows.Err(); err != nil {
		return domain.CampaignProgress{}, err
	}
	return domain.SummarizeRecipients(synthetic), nil
}

const suppressionSchema = `
CREATE TABLE IF NOT EXISTS campaign_suppressions (
    tenant_id TEXT NOT NULL,
    destination TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, destination)
);
`

type PostgresSuppressionRepository struct {
	db *sql.DB
}

func NewPostgresSuppressionRepository(db *sql.DB) *PostgresSuppressionRepository {
	return &PostgresSuppressionRepository{db: db}
}

func (r *PostgresSuppressionRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, suppressionSchema)
	return err
}

func (r *PostgresSuppressionRepository) IsSuppressed(ctx context.Context, tenantID, destination string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM campaign_suppressions WHERE tenant_id = $1 AND destination = $2)`,
		tenantID, strings.TrimSpace(destination),
	).Scan(&exists)
	return exists, err
}

func (r *PostgresSuppressionRepository) Suppress(ctx context.Context, tenantID, destination, reason string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO campaign_suppressions (tenant_id, destination, reason, created_at)
         VALUES ($1,$2,$3,$4)
         ON CONFLICT (tenant_id, destination) DO UPDATE SET reason = EXCLUDED.reason`,
		tenantID, strings.TrimSpace(destination), reason, time.Now().UTC(),
	)
	return err
}

func (r *PostgresSuppressionRepository) Release(ctx context.Context, tenantID, destination string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM campaign_suppressions WHERE tenant_id = $1 AND destination = $2`,
		tenantID, strings.TrimSpace(destination),
	)
	return err
}

// InMemorySuppressionRepository backs the in-memory deployment.
type InMemorySuppressionRepository struct {
	mu      sync.RWMutex
	blocked map[string]string
}

func NewInMemorySuppressionRepository() *InMemorySuppressionRepository {
	return &InMemorySuppressionRepository{blocked: map[string]string{}}
}

func (r *InMemorySuppressionRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemorySuppressionRepository) IsSuppressed(_ context.Context, tenantID, destination string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.blocked[tenantID+":"+strings.TrimSpace(destination)]
	return ok, nil
}

func (r *InMemorySuppressionRepository) Suppress(_ context.Context, tenantID, destination, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blocked[tenantID+":"+strings.TrimSpace(destination)] = reason
	return nil
}

func (r *InMemorySuppressionRepository) Release(_ context.Context, tenantID, destination string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.blocked, tenantID+":"+strings.TrimSpace(destination))
	return nil
}
