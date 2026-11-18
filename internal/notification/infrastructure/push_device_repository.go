package infrastructure

import (
	"context"
	"database/sql"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

var _ application.PushDeviceRepository = (*PostgresPushDeviceRepository)(nil)

const pushDevicesSchema = `
CREATE TABLE IF NOT EXISTS push_devices (
    token TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    platform TEXT NOT NULL,
    app_version TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS push_devices_tenant_user_idx ON push_devices (tenant_id, user_id);
`

type PostgresPushDeviceRepository struct {
	db *sql.DB
}

func NewPostgresPushDeviceRepository(db *sql.DB) *PostgresPushDeviceRepository {
	return &PostgresPushDeviceRepository{db: db}
}

func (r *PostgresPushDeviceRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, pushDevicesSchema)
	return err
}

// SaveDevice re-binds an existing token to whoever registered it last: a phone
// can change hands, and the old owner must stop receiving its pushes.
func (r *PostgresPushDeviceRepository) SaveDevice(ctx context.Context, device domain.PushDevice) error {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO push_devices (token, tenant_id, user_id, platform, app_version, created_at, last_seen_at)
        VALUES ($1,$2,$3,$4,$5,$6,$6)
        ON CONFLICT (token) DO UPDATE SET
            tenant_id = EXCLUDED.tenant_id, user_id = EXCLUDED.user_id, platform = EXCLUDED.platform,
            app_version = EXCLUDED.app_version, last_seen_at = EXCLUDED.last_seen_at`,
		device.Token, device.TenantID, device.UserID, device.Platform, device.AppVersion, device.LastSeenAt.UTC())
	return err
}

func (r *PostgresPushDeviceRepository) DeleteUserDevice(ctx context.Context, tenantID, userID, token string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM push_devices WHERE token = $1 AND tenant_id = $2 AND user_id = $3`, token, tenantID, userID)
	return err
}

func (r *PostgresPushDeviceRepository) DeleteToken(ctx context.Context, token string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM push_devices WHERE token = $1`, token)
	return err
}

func (r *PostgresPushDeviceRepository) TokensFor(ctx context.Context, tenantID, userID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT token FROM push_devices WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tokens []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (r *PostgresPushDeviceRepository) UsersWithDevices(ctx context.Context, tenantID string) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT user_id FROM push_devices WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	users := map[string]bool{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		users[userID] = true
	}
	return users, rows.Err()
}
