package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const whatsAppStatusFreshness = time.Minute

var errWhatsAppCipherMissing = errors.New("whatsapp account cipher is not configured")

// WhatsAppConnector keeps a connected number healthy after signup: it reads
// the number's state from Meta, retries webhook subscription and registers the
// number. Tokens are decrypted only inside it and never leave.
type WhatsAppConnector struct {
	repository    WhatsAppAccountRepository
	signup        WhatsAppEmbeddedSignupProvider
	cipher        WhatsAppTokenCipher
	clock         Clock
	defaultTenant string
}

func NewWhatsAppConnector(repository WhatsAppAccountRepository, signup WhatsAppEmbeddedSignupProvider, cipher WhatsAppTokenCipher, clock Clock, defaultTenant string) *WhatsAppConnector {
	return &WhatsAppConnector{repository: repository, signup: signup, cipher: cipher, clock: clock, defaultTenant: defaultTenant}
}

func (c *WhatsAppConnector) load(ctx context.Context, tenantID string) (domain.WhatsAppAccount, string, error) {
	resolved, err := resolveNotificationTenant(tenantID, c.defaultTenant)
	if err != nil {
		return domain.WhatsAppAccount{}, "", err
	}
	account, ok, err := c.repository.FindAccountByTenantID(ctx, resolved)
	if err != nil {
		return domain.WhatsAppAccount{}, "", err
	}
	if !ok {
		return domain.WhatsAppAccount{}, "", ErrWhatsAppAccountNotFound
	}
	token, err := c.decryptToken(account)
	return account, token, err
}

func (c *WhatsAppConnector) decryptToken(account domain.WhatsAppAccount) (string, error) {
	if c.cipher == nil {
		return "", errWhatsAppCipherMissing
	}
	token, err := c.cipher.Decrypt(account.EncryptedAccessToken, account.AccessTokenIV)
	if err != nil {
		return "", fmt.Errorf("decrypt whatsapp access token: %w", err)
	}
	return token, nil
}

// Refresh re-reads the number's profile, token and webhook state from Meta.
// A failing call is recorded on the account rather than returned, because the
// account's state is the answer.
func (c *WhatsAppConnector) Refresh(ctx context.Context, tenantID string) (domain.WhatsAppAccount, error) {
	account, token, err := c.load(ctx, tenantID)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	c.checkStatus(ctx, &account, token)
	return account, c.repository.UpdateAccountStatus(ctx, account)
}

// RefreshIfStale is for reads: it spends Graph calls at most once a minute and
// falls back to the stored account when Meta cannot be reached.
func (c *WhatsAppConnector) RefreshIfStale(ctx context.Context, account domain.WhatsAppAccount) domain.WhatsAppAccount {
	if account.LastCheckedAt != nil && c.clock.Now().Sub(*account.LastCheckedAt) < whatsAppStatusFreshness {
		return account
	}
	refreshed, err := c.Refresh(ctx, account.TenantID)
	if err != nil {
		return account
	}
	return refreshed
}

func (c *WhatsAppConnector) checkStatus(ctx context.Context, account *domain.WhatsAppAccount, token string) {
	now := c.clock.Now().UTC()
	account.LastCheckedAt = &now
	c.checkToken(ctx, account, token)
	c.checkProfile(ctx, account, token)
	if account.TokenStatus == domain.WhatsAppTokenValid {
		c.checkSubscription(ctx, account, token)
	}
}

func (c *WhatsAppConnector) checkToken(ctx context.Context, account *domain.WhatsAppAccount, token string) {
	info, err := c.signup.InspectToken(ctx, token)
	if err != nil {
		return
	}
	account.TokenStatus = domain.WhatsAppTokenValid
	if !info.Valid {
		account.TokenStatus = domain.WhatsAppTokenNeedsReconnect
	}
	account.TokenNeverExpires = info.NeverExpires
	account.TokenExpiresAt = info.ExpiresAt
}

func (c *WhatsAppConnector) checkProfile(ctx context.Context, account *domain.WhatsAppAccount, token string) {
	profile, err := c.signup.GetPhoneNumberProfile(ctx, token, account.PhoneNumberID)
	if err != nil {
		account.StatusError = domain.PlainErrorText(err)
		if apiErr, ok := domain.AsWhatsAppAPIError(err); ok && apiErr.IsTokenInvalid() {
			account.TokenStatus = domain.WhatsAppTokenNeedsReconnect
		}
		return
	}
	applyPhoneProfile(account, profile)
	account.StatusError = ""
}

func (c *WhatsAppConnector) checkSubscription(ctx context.Context, account *domain.WhatsAppAccount, token string) {
	subscribed, err := c.signup.IsAppSubscribed(ctx, token, account.BusinessAccountID)
	if err != nil {
		return
	}
	if !subscribed {
		account.WebhookSubscribedAt = nil
		account.WebhookError = "The app is not subscribed to this WhatsApp Business Account."
		return
	}
	if account.WebhookSubscribedAt == nil {
		now := c.clock.Now().UTC()
		account.WebhookSubscribedAt = &now
	}
	account.WebhookError = ""
}

func applyPhoneProfile(account *domain.WhatsAppAccount, profile domain.WhatsAppAccount) {
	account.DisplayPhoneNumber = firstNonEmpty(profile.DisplayPhoneNumber, account.DisplayPhoneNumber)
	account.VerifiedName = firstNonEmpty(profile.VerifiedName, account.VerifiedName)
	account.QualityRating = profile.QualityRating
	account.NumberStatus = profile.NumberStatus
	account.NameStatus = profile.NameStatus
	account.MessagingLimitTier = profile.MessagingLimitTier
	account.PlatformType = profile.PlatformType
}

// subscribe asks Meta to deliver the account's webhooks to this app and
// records the outcome either way.
func (c *WhatsAppConnector) subscribe(ctx context.Context, account *domain.WhatsAppAccount, token string) error {
	err := c.signup.SubscribeBusinessApp(ctx, token, account.BusinessAccountID)
	if err == nil {
		err = c.confirmSubscription(ctx, token, account.BusinessAccountID)
	}
	if err != nil {
		account.WebhookSubscribedAt = nil
		account.WebhookError = domain.PlainErrorText(err)
		return err
	}
	now := c.clock.Now().UTC()
	account.WebhookSubscribedAt = &now
	account.WebhookError = ""
	return nil
}

func (c *WhatsAppConnector) confirmSubscription(ctx context.Context, token, businessAccountID string) error {
	subscribed, err := c.signup.IsAppSubscribed(ctx, token, businessAccountID)
	if err != nil {
		return err
	}
	if !subscribed {
		return errors.New("Meta accepted the subscription but does not list the app for this account yet")
	}
	return nil
}

// Resubscribe retries the webhook subscription for a connected account.
func (c *WhatsAppConnector) Resubscribe(ctx context.Context, tenantID string) (domain.WhatsAppAccount, error) {
	account, token, err := c.load(ctx, tenantID)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	subscribeErr := c.subscribe(ctx, &account, token)
	c.noteTokenFailure(&account, subscribeErr)
	if err := c.repository.UpdateAccountStatus(ctx, account); err != nil {
		return domain.WhatsAppAccount{}, err
	}
	return account, subscribeErr
}

// Register retries the two-step registration a new number needs before the
// Cloud API will send from it.
func (c *WhatsAppConnector) Register(ctx context.Context, tenantID string) (domain.WhatsAppAccount, error) {
	account, token, err := c.load(ctx, tenantID)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	registerErr := c.register(ctx, &account, token)
	if err := c.repository.UpdateAccountStatus(ctx, account); err != nil {
		return domain.WhatsAppAccount{}, err
	}
	return account, registerErr
}

func (c *WhatsAppConnector) register(ctx context.Context, account *domain.WhatsAppAccount, token string) error {
	if account.Coexistence {
		return errors.New("a number that stays on the WhatsApp Business app is not registered here")
	}
	pin, err := c.pinFor(account)
	if err != nil {
		account.RegisterError = err.Error()
		return err
	}
	if err := c.signup.RegisterPhoneNumber(ctx, token, account.PhoneNumberID, pin); err != nil {
		account.RegisteredAt = nil
		account.RegisterError = domain.PlainErrorText(err)
		c.noteTokenFailure(account, err)
		return err
	}
	now := c.clock.Now().UTC()
	account.RegisteredAt = &now
	account.RegisterError = ""
	return nil
}

func (c *WhatsAppConnector) noteTokenFailure(account *domain.WhatsAppAccount, err error) {
	if apiErr, ok := domain.AsWhatsAppAPIError(err); ok && apiErr.IsTokenInvalid() {
		account.TokenStatus = domain.WhatsAppTokenNeedsReconnect
	}
}

// pinFor reuses the stored PIN so a repeat registration matches the first one.
func (c *WhatsAppConnector) pinFor(account *domain.WhatsAppAccount) (string, error) {
	if c.cipher == nil {
		return "", errWhatsAppCipherMissing
	}
	if account.EncryptedPIN != "" {
		if pin, err := c.cipher.Decrypt(account.EncryptedPIN, account.PINIV); err == nil {
			return pin, nil
		}
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("generate registration pin: %w", err)
	}
	pin := fmt.Sprintf("%06d", number.Int64())
	ciphertext, iv, err := c.cipher.Encrypt(pin)
	if err != nil {
		return "", err
	}
	account.EncryptedPIN, account.PINIV = ciphertext, iv
	return pin, nil
}

// CheckAll is the daily sweep: it catches a token that died with nobody
// sending, so the page shows reconnect before staff find out the hard way.
func (c *WhatsAppConnector) CheckAll(ctx context.Context) (checked int, err error) {
	accounts, err := c.repository.ListAccounts(ctx)
	if err != nil {
		return 0, err
	}
	for _, account := range accounts {
		if _, refreshErr := c.Refresh(ctx, account.TenantID); refreshErr != nil {
			err = refreshErr
			continue
		}
		checked++
	}
	return checked, err
}

// RefreshForBusinessAccount answers Meta's account-level webhook changes
// (quality, name review, restrictions) by re-reading the number.
func (c *WhatsAppConnector) RefreshForBusinessAccount(ctx context.Context, businessAccountID string) {
	account, ok, err := c.repository.FindAccountByBusinessAccountID(ctx, strings.TrimSpace(businessAccountID))
	if err != nil || !ok {
		return
	}
	_, _ = c.Refresh(ctx, account.TenantID)
}
