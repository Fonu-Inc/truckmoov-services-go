package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

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
`

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

func (r *PostgresWhatsAppAccountRepository) SaveAccount(ctx context.Context, account domain.WhatsAppAccount) (domain.WhatsAppAccount, error) {
	account.TenantID = strings.TrimSpace(account.TenantID)
	account.PhoneNumberID = strings.TrimSpace(account.PhoneNumberID)
	account.BusinessAccountID = strings.TrimSpace(account.BusinessAccountID)
	account.DisplayPhoneNumber = strings.TrimSpace(account.DisplayPhoneNumber)
	account.VerifiedName = strings.TrimSpace(account.VerifiedName)
	account.BusinessName = strings.TrimSpace(account.BusinessName)
	account.EncryptedAccessToken = strings.TrimSpace(account.EncryptedAccessToken)
	account.AccessTokenIV = strings.TrimSpace(account.AccessTokenIV)
	if account.ConnectedAt.IsZero() {
		account.ConnectedAt = time.Now().UTC()
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = account.ConnectedAt
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = account.ConnectedAt
	}

	query := `
INSERT INTO whatsapp_accounts (
    id,
    tenant_id,
    phone_number_id,
    business_account_id,
    display_phone_number,
    verified_name,
    business_name,
    encrypted_access_token,
    access_token_iv,
    webhook_subscribed_at,
    connected_at,
    created_at,
    updated_at
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13
)
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
    updated_at = EXCLUDED.updated_at
RETURNING id, tenant_id, phone_number_id, business_account_id, display_phone_number, verified_name, business_name,
    encrypted_access_token, access_token_iv, webhook_subscribed_at, connected_at, created_at, updated_at
`

	row := r.db.QueryRowContext(ctx, query,
		account.ID,
		account.TenantID,
		account.PhoneNumberID,
		account.BusinessAccountID,
		account.DisplayPhoneNumber,
		account.VerifiedName,
		account.BusinessName,
		account.EncryptedAccessToken,
		account.AccessTokenIV,
		nullableWhatsAppAccountTime(account.WebhookSubscribedAt),
		account.ConnectedAt.UTC(),
		account.CreatedAt.UTC(),
		account.UpdatedAt.UTC(),
	)
	stored, err := scanWhatsAppAccount(row)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	return stored, nil
}

func (r *PostgresWhatsAppAccountRepository) FindAccountByTenantID(ctx context.Context, tenantID string) (domain.WhatsAppAccount, bool, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, tenant_id, phone_number_id, business_account_id, display_phone_number, verified_name, business_name,
    encrypted_access_token, access_token_iv, webhook_subscribed_at, connected_at, created_at, updated_at
FROM whatsapp_accounts
WHERE tenant_id = $1
`, strings.TrimSpace(tenantID))
	account, err := scanWhatsAppAccount(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.WhatsAppAccount{}, false, nil
		}
		return domain.WhatsAppAccount{}, false, err
	}
	return account, true, nil
}

func (r *PostgresWhatsAppAccountRepository) FindAccountByPhoneNumberID(ctx context.Context, phoneNumberID string) (domain.WhatsAppAccount, bool, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, tenant_id, phone_number_id, business_account_id, display_phone_number, verified_name, business_name,
    encrypted_access_token, access_token_iv, webhook_subscribed_at, connected_at, created_at, updated_at
FROM whatsapp_accounts
WHERE phone_number_id = $1
`, strings.TrimSpace(phoneNumberID))
	account, err := scanWhatsAppAccount(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.WhatsAppAccount{}, false, nil
		}
		return domain.WhatsAppAccount{}, false, err
	}
	return account, true, nil
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
	var webhookSubscribedAt sql.NullTime
	if err := row.Scan(
		&account.ID,
		&account.TenantID,
		&account.PhoneNumberID,
		&account.BusinessAccountID,
		&account.DisplayPhoneNumber,
		&account.VerifiedName,
		&account.BusinessName,
		&account.EncryptedAccessToken,
		&account.AccessTokenIV,
		&webhookSubscribedAt,
		&account.ConnectedAt,
		&account.CreatedAt,
		&account.UpdatedAt,
	); err != nil {
		return domain.WhatsAppAccount{}, err
	}
	if webhookSubscribedAt.Valid {
		value := webhookSubscribedAt.Time.UTC()
		account.WebhookSubscribedAt = &value
	}
	account.ConnectedAt = account.ConnectedAt.UTC()
	account.CreatedAt = account.CreatedAt.UTC()
	account.UpdatedAt = account.UpdatedAt.UTC()
	return account, nil
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
	if existing, ok := r.byTenantID[account.TenantID]; ok {
		account.CreatedAt = existing.CreatedAt
		if existing.WebhookSubscribedAt != nil && account.WebhookSubscribedAt == nil {
			account.WebhookSubscribedAt = existing.WebhookSubscribedAt
		}
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
