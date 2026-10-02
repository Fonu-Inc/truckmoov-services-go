package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

const whatsAppAccountSchema = `
CREATE TABLE IF NOT EXISTS whatsapp_accounts (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL UNIQUE,
    phone_number_id TEXT NOT NULL UNIQUE,
    business_account_id TEXT NOT NULL,
    display_phone_number TEXT NOT NULL DEFAULT '',
    verified_name TEXT NOT NULL DEFAULT '',
    business_name TEXT NOT NULL DEFAULT '',
    encrypted_access_token TEXT NOT NULL,
    access_token_iv TEXT NOT NULL,
    webhook_subscribed_at TIMESTAMPTZ NULL,
    connected_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_whatsapp_accounts_business_account_id ON whatsapp_accounts (business_account_id);
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS quality_rating TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS number_status TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS name_status TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS messaging_limit_tier TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS platform_type TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS coexistence BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS token_status TEXT NOT NULL DEFAULT 'valid';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS token_expires_at TIMESTAMPTZ NULL;
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS token_never_expires BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS status_error TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS webhook_error TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS register_error TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS registered_at TIMESTAMPTZ NULL;
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS last_checked_at TIMESTAMPTZ NULL;
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS encrypted_pin TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS pin_iv TEXT NOT NULL DEFAULT '';
ALTER TABLE whatsapp_accounts ADD COLUMN IF NOT EXISTS key_id TEXT NOT NULL DEFAULT '';
`

const whatsAppAccountColumns = `id, tenant_id, phone_number_id, business_account_id, display_phone_number, verified_name, business_name,
    encrypted_access_token, access_token_iv, webhook_subscribed_at, connected_at, created_at, updated_at,
    quality_rating, number_status, name_status, messaging_limit_tier, platform_type, coexistence,
    token_status, token_expires_at, token_never_expires, status_error, webhook_error, register_error,
    registered_at, last_checked_at, encrypted_pin, pin_iv, key_id`

type PostgresWhatsAppAccountRepository struct {
	db *sql.DB
}

func NewPostgresWhatsAppAccountRepository(db *sql.DB) *PostgresWhatsAppAccountRepository {
	return &PostgresWhatsAppAccountRepository{db: db}
}

func (r *PostgresWhatsAppAccountRepository) EnsureSchema(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, whatsAppAccountSchema); err != nil {
		return fmt.Errorf("ensure whatsapp account schema: %w", err)
	}
	return nil
}

func normalizeWhatsAppAccount(account domain.WhatsAppAccount) domain.WhatsAppAccount {
	account.TenantID = strings.TrimSpace(account.TenantID)
	account.PhoneNumberID = strings.TrimSpace(account.PhoneNumberID)
	account.BusinessAccountID = strings.TrimSpace(account.BusinessAccountID)
	account.DisplayPhoneNumber = strings.TrimSpace(account.DisplayPhoneNumber)
	account.VerifiedName = strings.TrimSpace(account.VerifiedName)
	account.BusinessName = strings.TrimSpace(account.BusinessName)
	account.EncryptedAccessToken = strings.TrimSpace(account.EncryptedAccessToken)
	account.AccessTokenIV = strings.TrimSpace(account.AccessTokenIV)
	if account.TokenStatus == "" {
		account.TokenStatus = domain.WhatsAppTokenValid
	}
	if account.ConnectedAt.IsZero() {
		account.ConnectedAt = time.Now().UTC()
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = account.ConnectedAt
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.ConnectedAt
	}
	return account
}

func (r *PostgresWhatsAppAccountRepository) SaveAccount(ctx context.Context, account domain.WhatsAppAccount) (domain.WhatsAppAccount, error) {
	account = normalizeWhatsAppAccount(account)
	query := `
INSERT INTO whatsapp_accounts (` + whatsAppAccountColumns + `)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)
ON CONFLICT (tenant_id) DO UPDATE SET
    phone_number_id = EXCLUDED.phone_number_id,
    business_account_id = EXCLUDED.business_account_id,
    display_phone_number = EXCLUDED.display_phone_number,
    verified_name = EXCLUDED.verified_name,
    business_name = EXCLUDED.business_name,
    encrypted_access_token = EXCLUDED.encrypted_access_token,
    access_token_iv = EXCLUDED.access_token_iv,
    webhook_subscribed_at = EXCLUDED.webhook_subscribed_at,
    connected_at = EXCLUDED.connected_at,
    updated_at = EXCLUDED.updated_at,
    quality_rating = EXCLUDED.quality_rating,
    number_status = EXCLUDED.number_status,
    name_status = EXCLUDED.name_status,
    messaging_limit_tier = EXCLUDED.messaging_limit_tier,
    platform_type = EXCLUDED.platform_type,
    coexistence = EXCLUDED.coexistence,
    token_status = EXCLUDED.token_status,
    token_expires_at = EXCLUDED.token_expires_at,
    token_never_expires = EXCLUDED.token_never_expires,
    status_error = EXCLUDED.status_error,
    webhook_error = EXCLUDED.webhook_error,
    register_error = EXCLUDED.register_error,
    registered_at = EXCLUDED.registered_at,
    last_checked_at = EXCLUDED.last_checked_at,
    encrypted_pin = EXCLUDED.encrypted_pin,
    pin_iv = EXCLUDED.pin_iv,
    key_id = EXCLUDED.key_id
RETURNING ` + whatsAppAccountColumns

	row := r.db.QueryRowContext(ctx, query,
		account.ID, account.TenantID, account.PhoneNumberID, account.BusinessAccountID,
		account.DisplayPhoneNumber, account.VerifiedName, account.BusinessName,
		account.EncryptedAccessToken, account.AccessTokenIV,
		nullableWhatsAppAccountTime(account.WebhookSubscribedAt),
		account.ConnectedAt.UTC(), account.CreatedAt.UTC(), account.UpdatedAt.UTC(),
		account.QualityRating, account.NumberStatus, account.NameStatus, account.MessagingLimitTier, account.PlatformType,
		account.Coexistence, account.TokenStatus, nullableWhatsAppAccountTime(account.TokenExpiresAt), account.TokenNeverExpires,
		account.StatusError, account.WebhookError, account.RegisterError,
		nullableWhatsAppAccountTime(account.RegisteredAt), nullableWhatsAppAccountTime(account.LastCheckedAt),
		account.EncryptedPIN, account.PINIV, account.KeyID,
	)
	stored, err := scanWhatsAppAccount(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.WhatsAppAccount{}, domain.ErrWhatsAppNumberInUse
		}
		return domain.WhatsAppAccount{}, err
	}
	return stored, nil
}

// UpdateAccountStatus rewrites only what a status check learns. It matches the
// phone number too, so a check that raced a reconnect to another number
// changes nothing.
func (r *PostgresWhatsAppAccountRepository) UpdateAccountStatus(ctx context.Context, account domain.WhatsAppAccount) error {
	account = normalizeWhatsAppAccount(account)
	_, err := r.db.ExecContext(ctx, `
UPDATE whatsapp_accounts SET
    display_phone_number = $3, verified_name = $4, quality_rating = $5, number_status = $6, name_status = $7,
    messaging_limit_tier = $8, platform_type = $9, token_status = $10, token_expires_at = $11,
    token_never_expires = $12, status_error = $13, webhook_error = $14, register_error = $15,
    webhook_subscribed_at = $16, registered_at = $17, last_checked_at = $18,
    encrypted_pin = $19, pin_iv = $20, updated_at = $21
WHERE tenant_id = $1 AND phone_number_id = $2`,
		account.TenantID, account.PhoneNumberID,
		account.DisplayPhoneNumber, account.VerifiedName, account.QualityRating, account.NumberStatus, account.NameStatus,
		account.MessagingLimitTier, account.PlatformType, account.TokenStatus, nullableWhatsAppAccountTime(account.TokenExpiresAt),
		account.TokenNeverExpires, account.StatusError, account.WebhookError, account.RegisterError,
		nullableWhatsAppAccountTime(account.WebhookSubscribedAt), nullableWhatsAppAccountTime(account.RegisteredAt),
		nullableWhatsAppAccountTime(account.LastCheckedAt), account.EncryptedPIN, account.PINIV, time.Now().UTC(),
	)
	return err
}

func (r *PostgresWhatsAppAccountRepository) SetTokenStatus(ctx context.Context, tenantID, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE whatsapp_accounts SET token_status = $2, updated_at = $3 WHERE tenant_id = $1`, strings.TrimSpace(tenantID), status, time.Now().UTC())
	return err
}

func (r *PostgresWhatsAppAccountRepository) findOne(ctx context.Context, where string, arg string) (domain.WhatsAppAccount, bool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+whatsAppAccountColumns+` FROM whatsapp_accounts WHERE `+where, strings.TrimSpace(arg))
	account, err := scanWhatsAppAccount(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.WhatsAppAccount{}, false, nil
		}
		return domain.WhatsAppAccount{}, false, err
	}
	return account, true, nil
}

func (r *PostgresWhatsAppAccountRepository) FindAccountByTenantID(ctx context.Context, tenantID string) (domain.WhatsAppAccount, bool, error) {
	return r.findOne(ctx, "tenant_id = $1", tenantID)
}

func (r *PostgresWhatsAppAccountRepository) FindAccountByPhoneNumberID(ctx context.Context, phoneNumberID string) (domain.WhatsAppAccount, bool, error) {
	return r.findOne(ctx, "phone_number_id = $1", phoneNumberID)
}

func (r *PostgresWhatsAppAccountRepository) FindAccountByBusinessAccountID(ctx context.Context, businessAccountID string) (domain.WhatsAppAccount, bool, error) {
	return r.findOne(ctx, "business_account_id = $1 ORDER BY connected_at DESC LIMIT 1", businessAccountID)
}

func (r *PostgresWhatsAppAccountRepository) ListAccounts(ctx context.Context) ([]domain.WhatsAppAccount, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+whatsAppAccountColumns+` FROM whatsapp_accounts ORDER BY connected_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []domain.WhatsAppAccount
	for rows.Next() {
		account, err := scanWhatsAppAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (r *PostgresWhatsAppAccountRepository) DeleteAccountByTenantAndPhoneNumberID(ctx context.Context, tenantID, phoneNumberID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM whatsapp_accounts WHERE tenant_id = $1 AND phone_number_id = $2`, strings.TrimSpace(tenantID), strings.TrimSpace(phoneNumberID))
	return err
}

type whatsAppAccountScanner interface {
	Scan(dest ...any) error
}

func scanWhatsAppAccount(row whatsAppAccountScanner) (domain.WhatsAppAccount, error) {
	var account domain.WhatsAppAccount
	var webhookSubscribedAt, tokenExpiresAt, registeredAt, lastCheckedAt sql.NullTime
	if err := row.Scan(
		&account.ID, &account.TenantID, &account.PhoneNumberID, &account.BusinessAccountID,
		&account.DisplayPhoneNumber, &account.VerifiedName, &account.BusinessName,
		&account.EncryptedAccessToken, &account.AccessTokenIV, &webhookSubscribedAt,
		&account.ConnectedAt, &account.CreatedAt, &account.UpdatedAt,
		&account.QualityRating, &account.NumberStatus, &account.NameStatus, &account.MessagingLimitTier, &account.PlatformType,
		&account.Coexistence, &account.TokenStatus, &tokenExpiresAt, &account.TokenNeverExpires,
		&account.StatusError, &account.WebhookError, &account.RegisterError,
		&registeredAt, &lastCheckedAt, &account.EncryptedPIN, &account.PINIV, &account.KeyID,
	); err != nil {
		return domain.WhatsAppAccount{}, err
	}
	account.WebhookSubscribedAt = utcTimePointer(webhookSubscribedAt)
	account.TokenExpiresAt = utcTimePointer(tokenExpiresAt)
	account.RegisteredAt = utcTimePointer(registeredAt)
	account.LastCheckedAt = utcTimePointer(lastCheckedAt)
	account.ConnectedAt = account.ConnectedAt.UTC()
	account.CreatedAt = account.CreatedAt.UTC()
	account.UpdatedAt = account.UpdatedAt.UTC()
	return account, nil
}

func utcTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}

func nullableWhatsAppAccountTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

type InMemoryWhatsAppAccountRepository struct {
	mu         sync.RWMutex
	byTenantID map[string]domain.WhatsAppAccount
	byPhoneID  map[string]string
}

func NewInMemoryWhatsAppAccountRepository() *InMemoryWhatsAppAccountRepository {
	return &InMemoryWhatsAppAccountRepository{
		byTenantID: make(map[string]domain.WhatsAppAccount),
		byPhoneID:  make(map[string]string),
	}
}

func (r *InMemoryWhatsAppAccountRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemoryWhatsAppAccountRepository) SaveAccount(_ context.Context, account domain.WhatsAppAccount) (domain.WhatsAppAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account = normalizeWhatsAppAccount(account)
	if owner, taken := r.byPhoneID[account.PhoneNumberID]; taken && owner != account.TenantID {
		return domain.WhatsAppAccount{}, domain.ErrWhatsAppNumberInUse
	}
	if existing, ok := r.byTenantID[account.TenantID]; ok {
		account.CreatedAt = existing.CreatedAt
		delete(r.byPhoneID, existing.PhoneNumberID)
	}
	r.byTenantID[account.TenantID] = account
	r.byPhoneID[account.PhoneNumberID] = account.TenantID
	return account, nil
}

func (r *InMemoryWhatsAppAccountRepository) FindAccountByTenantID(_ context.Context, tenantID string) (domain.WhatsAppAccount, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	account, ok := r.byTenantID[strings.TrimSpace(tenantID)]
	return account, ok, nil
}

func (r *InMemoryWhatsAppAccountRepository) FindAccountByPhoneNumberID(_ context.Context, phoneNumberID string) (domain.WhatsAppAccount, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tenantID, ok := r.byPhoneID[strings.TrimSpace(phoneNumberID)]
	if !ok {
		return domain.WhatsAppAccount{}, false, nil
	}
	account, ok := r.byTenantID[tenantID]
	return account, ok, nil
}

func (r *InMemoryWhatsAppAccountRepository) DeleteAccountByTenantAndPhoneNumberID(_ context.Context, tenantID, phoneNumberID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byTenantID, strings.TrimSpace(tenantID))
	delete(r.byPhoneID, strings.TrimSpace(phoneNumberID))
	return nil
}

func (r *InMemoryWhatsAppAccountRepository) UpdateAccountStatus(_ context.Context, account domain.WhatsAppAccount) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.byTenantID[account.TenantID]
	if !ok || stored.PhoneNumberID != account.PhoneNumberID {
		return nil
	}
	account.EncryptedAccessToken = stored.EncryptedAccessToken
	account.AccessTokenIV = stored.AccessTokenIV
	account.CreatedAt = stored.CreatedAt
	account.ConnectedAt = stored.ConnectedAt
	r.byTenantID[account.TenantID] = account
	return nil
}

func (r *InMemoryWhatsAppAccountRepository) SetTokenStatus(_ context.Context, tenantID, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if account, ok := r.byTenantID[strings.TrimSpace(tenantID)]; ok {
		account.TokenStatus = status
		r.byTenantID[account.TenantID] = account
	}
	return nil
}

func (r *InMemoryWhatsAppAccountRepository) FindAccountByBusinessAccountID(_ context.Context, businessAccountID string) (domain.WhatsAppAccount, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, account := range r.byTenantID {
		if account.BusinessAccountID == strings.TrimSpace(businessAccountID) {
			return account, true, nil
		}
	}
	return domain.WhatsAppAccount{}, false, nil
}

func (r *InMemoryWhatsAppAccountRepository) ListAccounts(context.Context) ([]domain.WhatsAppAccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	accounts := make([]domain.WhatsAppAccount, 0, len(r.byTenantID))
	for _, account := range r.byTenantID {
		accounts = append(accounts, account)
	}
	return accounts, nil
}
