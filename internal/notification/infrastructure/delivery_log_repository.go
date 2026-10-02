package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const deliveryLogSchema = `
CREATE TABLE IF NOT EXISTS notification_delivery_log (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    notification_type TEXT NOT NULL DEFAULT 'generic',
    channel TEXT NOT NULL,
    recipient TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    provider_message_id TEXT NULL,
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_notification_delivery_log_tenant
    ON notification_delivery_log (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notification_delivery_log_type
    ON notification_delivery_log (tenant_id, notification_type, created_at DESC);

-- Delivery state arrives from the provider after the row exists, and it arrives
-- keyed on the provider's own id.
CREATE INDEX IF NOT EXISTS idx_notification_delivery_log_provider_message
    ON notification_delivery_log (provider_message_id);
`

type PostgresDeliveryLogRepository struct {
	db *sql.DB
}

func NewPostgresDeliveryLogRepository(db *sql.DB) *PostgresDeliveryLogRepository {
	return &PostgresDeliveryLogRepository{db: db}
}

func (r *PostgresDeliveryLogRepository) EnsureSchema(ctx context.Context) error {
	if r.db == nil {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, deliveryLogSchema); err != nil {
		return fmt.Errorf("ensure delivery log schema: %w", err)
	}
	return nil
}

func (r *PostgresDeliveryLogRepository) Append(ctx context.Context, entry domain.DeliveryLogEntry) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO notification_delivery_log
           (id, tenant_id, notification_type, channel, recipient, subject, content,
            status, provider_message_id, failure_reason, created_at, updated_at, delivered_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
         ON CONFLICT (id) DO UPDATE SET
            status = EXCLUDED.status,
            provider_message_id = COALESCE(EXCLUDED.provider_message_id, notification_delivery_log.provider_message_id),
            failure_reason = EXCLUDED.failure_reason,
            updated_at = EXCLUDED.updated_at,
            delivered_at = COALESCE(EXCLUDED.delivered_at, notification_delivery_log.delivered_at)
         WHERE notification_delivery_log.tenant_id = EXCLUDED.tenant_id
           AND notification_delivery_log.status = 'failed'`,
		entry.ID, entry.TenantID, entry.NotificationType, entry.Channel, entry.Recipient,
		entry.Subject, entry.Content, entry.Status, nullableProviderMessageID(entry.ProviderMessageID),
		entry.FailureReason, entry.CreatedAt, entry.UpdatedAt, entry.DeliveredAt,
	)
	if err != nil {
		return fmt.Errorf("append delivery log entry: %w", err)
	}
	return nil
}

// UpdateStatusByProviderMessageID lands the state a provider reports after the
// send. The row already exists, so this only moves it forward.
func (r *PostgresDeliveryLogRepository) UpdateStatusByProviderMessageID(
	ctx context.Context,
	providerMessageID, status, failureReason string,
	occurredAt time.Time,
) (bool, error) {
	if r.db == nil || strings.TrimSpace(providerMessageID) == "" {
		return false, nil
	}

	var deliveredAt *time.Time
	if status == domain.StatusDelivered {
		deliveredAt = &occurredAt
	}

	result, err := r.db.ExecContext(
		ctx,
		`UPDATE notification_delivery_log
            SET status = $2,
                failure_reason = $3,
                delivered_at = COALESCE($4, delivered_at),
                updated_at = $5
          WHERE provider_message_id = $1`,
		providerMessageID, status, failureReason, deliveredAt, occurredAt,
	)
	if err != nil {
		return false, fmt.Errorf("update delivery log status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, nil
	}
	return affected > 0, nil
}

func (r *PostgresDeliveryLogRepository) List(
	ctx context.Context,
	tenantID string,
	filters domain.DeliveryLogFilters,
) ([]domain.DeliveryLogEntry, int, error) {
	if r.db == nil {
		return nil, 0, nil
	}

	conditions := []string{"tenant_id = $1"}
	args := []any{tenantID}
	if strings.TrimSpace(filters.NotificationType) != "" {
		args = append(args, filters.NotificationType)
		conditions = append(conditions, fmt.Sprintf("notification_type = $%d", len(args)))
	}
	if strings.TrimSpace(filters.Channel) != "" {
		args = append(args, filters.Channel)
		conditions = append(conditions, fmt.Sprintf("channel = $%d", len(args)))
	}
	if strings.TrimSpace(filters.Status) != "" {
		args = append(args, filters.Status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	where := "WHERE " + strings.Join(conditions, " AND ")

	var total int
	if err := r.db.QueryRowContext(
		ctx, "SELECT COUNT(*) FROM notification_delivery_log "+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count delivery log: %w", err)
	}

	limit := filters.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit, maxInt(filters.Offset, 0))

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, tenant_id, notification_type, channel, recipient, subject, content,
                status, COALESCE(provider_message_id, ''), failure_reason,
                created_at, updated_at, delivered_at
           FROM notification_delivery_log `+where+
			fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list delivery log: %w", err)
	}
	defer rows.Close()

	entries := make([]domain.DeliveryLogEntry, 0, limit)
	for rows.Next() {
		var entry domain.DeliveryLogEntry
		if err := rows.Scan(
			&entry.ID, &entry.TenantID, &entry.NotificationType, &entry.Channel, &entry.Recipient,
			&entry.Subject, &entry.Content, &entry.Status, &entry.ProviderMessageID,
			&entry.FailureReason, &entry.CreatedAt, &entry.UpdatedAt, &entry.DeliveredAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan delivery log entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, total, rows.Err()
}

// DeleteOlderThan applies the retention window. Entries hold message content
// sent to a person, so keeping them for ever is a choice that has to be made
// deliberately rather than by default.
func (r *PostgresDeliveryLogRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if r.db == nil {
		return 0, nil
	}
	result, err := r.db.ExecContext(
		ctx, "DELETE FROM notification_delivery_log WHERE created_at < $1", cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("prune delivery log: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return affected, nil
}

func nullableProviderMessageID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// InMemoryDeliveryLogRepository backs the service when it runs without
// Postgres. It applies the same filters and ordering as the Postgres
// implementation, so a caller sees one behaviour whichever backend is in use.
type InMemoryDeliveryLogRepository struct {
	mu      sync.RWMutex
	entries []domain.DeliveryLogEntry
}

func NewInMemoryDeliveryLogRepository() *InMemoryDeliveryLogRepository {
	return &InMemoryDeliveryLogRepository{}
}

func (r *InMemoryDeliveryLogRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemoryDeliveryLogRepository) Append(_ context.Context, entry domain.DeliveryLogEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return nil
}

func (r *InMemoryDeliveryLogRepository) UpdateStatusByProviderMessageID(
	_ context.Context,
	providerMessageID, status, failureReason string,
	occurredAt time.Time,
) (bool, error) {
	if strings.TrimSpace(providerMessageID) == "" {
		return false, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var deliveredAt *time.Time
	if status == domain.StatusDelivered {
		stamped := occurredAt
		deliveredAt = &stamped
	}

	updated := false
	for i := range r.entries {
		if r.entries[i].ProviderMessageID != providerMessageID {
			continue
		}
		r.entries[i].Status = status
		r.entries[i].FailureReason = failureReason
		r.entries[i].UpdatedAt = occurredAt
		if deliveredAt != nil {
			r.entries[i].DeliveredAt = deliveredAt
		}
		updated = true
	}
	return updated, nil
}

func (r *InMemoryDeliveryLogRepository) List(
	_ context.Context, tenantID string, filters domain.DeliveryLogFilters,
) ([]domain.DeliveryLogEntry, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matching := make([]domain.DeliveryLogEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		if entry.TenantID != tenantID {
			continue
		}
		if filters.NotificationType != "" && entry.NotificationType != filters.NotificationType {
			continue
		}
		if filters.Channel != "" && entry.Channel != filters.Channel {
			continue
		}
		if filters.Status != "" && entry.Status != filters.Status {
			continue
		}
		matching = append(matching, entry)
	}

	// Newest first, matching the Postgres ORDER BY created_at DESC.
	sort.SliceStable(matching, func(i, j int) bool {
		return matching[i].CreatedAt.After(matching[j].CreatedAt)
	})

	total := len(matching)
	offset := maxInt(filters.Offset, 0)
	if offset >= total {
		return []domain.DeliveryLogEntry{}, total, nil
	}
	limit := filters.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return matching[offset:end], total, nil
}

func (r *InMemoryDeliveryLogRepository) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make([]domain.DeliveryLogEntry, 0, len(r.entries))
	var removed int64
	for _, entry := range r.entries {
		if entry.CreatedAt.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, entry)
	}
	r.entries = kept
	return removed, nil
}
