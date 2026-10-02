package infrastructure

import (
	"context"
	"database/sql"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

var _ application.NotificationPreferencesRepository = (*PostgresNotificationPreferencesRepository)(nil)

const notificationPreferencesSchema = `
CREATE TABLE IF NOT EXISTS notification_preferences (
    tenant_id TEXT PRIMARY KEY,
    email BOOLEAN NOT NULL DEFAULT TRUE,
    whatsapp BOOLEAN NOT NULL DEFAULT FALSE,
    sms BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL
);
`

type PostgresNotificationPreferencesRepository struct {
	db *sql.DB
}

func NewPostgresNotificationPreferencesRepository(db *sql.DB) *PostgresNotificationPreferencesRepository {
	return &PostgresNotificationPreferencesRepository{db: db}
}

func (r *PostgresNotificationPreferencesRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, notificationPreferencesSchema)
	return err
}

func (r *PostgresNotificationPreferencesRepository) FindPreferences(ctx context.Context, tenantID string) (domain.NotificationPreferences, bool, error) {
	var preferences domain.NotificationPreferences
	err := r.db.QueryRowContext(ctx,
		`SELECT tenant_id, email, whatsapp, sms, updated_at FROM notification_preferences WHERE tenant_id = $1`, tenantID,
	).Scan(&preferences.TenantID, &preferences.Email, &preferences.WhatsApp, &preferences.SMS, &preferences.UpdatedAt)
	if err == sql.ErrNoRows {
		return domain.NotificationPreferences{}, false, nil
	}
	if err != nil {
		return domain.NotificationPreferences{}, false, err
	}
	return preferences, true, nil
}

func (r *PostgresNotificationPreferencesRepository) SavePreferences(ctx context.Context, preferences domain.NotificationPreferences) error {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO notification_preferences (tenant_id, email, whatsapp, sms, updated_at)
        VALUES ($1,$2,$3,$4,$5)
        ON CONFLICT (tenant_id) DO UPDATE SET
            email = EXCLUDED.email, whatsapp = EXCLUDED.whatsapp, sms = EXCLUDED.sms, updated_at = EXCLUDED.updated_at`,
		preferences.TenantID, preferences.Email, preferences.WhatsApp, preferences.SMS, preferences.UpdatedAt.UTC())
	return err
}
