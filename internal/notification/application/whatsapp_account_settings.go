package application

import (
	"context"
	"errors"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

var (
	ErrWhatsAppAccountPhoneNumberIDRequired = errors.New("phone number id is required")
	ErrWhatsAppAccountBusinessIDRequired    = errors.New("business account id is required")
	ErrWhatsAppAccountCodeRequired          = errors.New("embedded signup code is required")
	ErrWhatsAppAccountNotFound              = errors.New("whatsapp account is not connected")
)

type ConnectWhatsAppAccountCommand struct {
	TenantID          string
	PhoneNumberID     string
	BusinessAccountID string
	Code              string
}

type GetWhatsAppAccountQuery struct {
	TenantID string
}

type DisconnectWhatsAppAccountCommand struct {
	TenantID      string
	PhoneNumberID string
}

type ConnectWhatsAppAccountUseCase struct {
	repository    WhatsAppAccountRepository
	signup        WhatsAppEmbeddedSignupProvider
	cipher        WhatsAppTokenCipher
	clock         Clock
	idGenerator   IDGenerator
	defaultTenant string
}

type GetWhatsAppAccountUseCase struct {
	repository    WhatsAppAccountRepository
	defaultTenant string
}

type DisconnectWhatsAppAccountUseCase struct {
	repository    WhatsAppAccountRepository
	defaultTenant string
}

func NewConnectWhatsAppAccountUseCase(repository WhatsAppAccountRepository, signup WhatsAppEmbeddedSignupProvider, cipher WhatsAppTokenCipher, clock Clock, idGenerator IDGenerator, defaultTenant string) *ConnectWhatsAppAccountUseCase {
	return &ConnectWhatsAppAccountUseCase{repository: repository, signup: signup, cipher: cipher, clock: clock, idGenerator: idGenerator, defaultTenant: defaultTenant}
}

func NewGetWhatsAppAccountUseCase(repository WhatsAppAccountRepository, defaultTenant string) *GetWhatsAppAccountUseCase {
	return &GetWhatsAppAccountUseCase{repository: repository, defaultTenant: defaultTenant}
}

func NewDisconnectWhatsAppAccountUseCase(repository WhatsAppAccountRepository, defaultTenant string) *DisconnectWhatsAppAccountUseCase {
	return &DisconnectWhatsAppAccountUseCase{repository: repository, defaultTenant: defaultTenant}
}

func (u *ConnectWhatsAppAccountUseCase) Execute(ctx context.Context, command ConnectWhatsAppAccountCommand) (domain.WhatsAppAccount, error) {
	tenantID, err := resolveNotificationTenant(command.TenantID, u.defaultTenant)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	phoneNumberID := strings.TrimSpace(command.PhoneNumberID)
	if phoneNumberID == "" {
		return domain.WhatsAppAccount{}, ErrWhatsAppAccountPhoneNumberIDRequired
	}
	businessAccountID := strings.TrimSpace(command.BusinessAccountID)
	if businessAccountID == "" {
		return domain.WhatsAppAccount{}, ErrWhatsAppAccountBusinessIDRequired
	}
	code := strings.TrimSpace(command.Code)
	if code == "" {
		return domain.WhatsAppAccount{}, ErrWhatsAppAccountCodeRequired
	}
	if u.signup == nil {
		return domain.WhatsAppAccount{}, errors.New("whatsapp embedded signup is not configured")
	}
	if u.cipher == nil {
		return domain.WhatsAppAccount{}, errors.New("whatsapp account cipher is not configured")
	}

	accessToken, err := u.signup.ExchangeCodeForLongLivedToken(ctx, code)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	ciphertext, iv, err := u.cipher.Encrypt(accessToken)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}

	now := u.clock.Now().UTC()
	account := domain.WhatsAppAccount{
		ID:                   u.idGenerator.New("wacfg"),
		TenantID:             tenantID,
		PhoneNumberID:        phoneNumberID,
		BusinessAccountID:    businessAccountID,
		EncryptedAccessToken: ciphertext,
		AccessTokenIV:        iv,
		ConnectedAt:          now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	if phoneProfile, err := u.signup.GetPhoneNumberProfile(ctx, accessToken, phoneNumberID); err == nil {
		account.DisplayPhoneNumber = strings.TrimSpace(phoneProfile.DisplayPhoneNumber)
		account.VerifiedName = strings.TrimSpace(phoneProfile.VerifiedName)
	}
	if businessProfile, err := u.signup.GetBusinessProfile(ctx, accessToken, businessAccountID); err == nil {
		account.BusinessName = strings.TrimSpace(businessProfile.BusinessName)
	}
	if err := u.signup.SubscribeBusinessApp(ctx, accessToken, businessAccountID); err == nil {
		account.WebhookSubscribedAt = timePointer(now)
	}

	return u.repository.SaveAccount(ctx, account)
}

func (u *GetWhatsAppAccountUseCase) Execute(ctx context.Context, query GetWhatsAppAccountQuery) (domain.WhatsAppAccount, bool, error) {
	tenantID, err := resolveNotificationTenant(query.TenantID, u.defaultTenant)
	if err != nil {
		return domain.WhatsAppAccount{}, false, err
	}
	return u.repository.FindAccountByTenantID(ctx, tenantID)
}

func (u *DisconnectWhatsAppAccountUseCase) Execute(ctx context.Context, command DisconnectWhatsAppAccountCommand) error {
	tenantID, err := resolveNotificationTenant(command.TenantID, u.defaultTenant)
	if err != nil {
		return err
	}
	phoneNumberID := strings.TrimSpace(command.PhoneNumberID)
	if phoneNumberID == "" {
		return ErrWhatsAppAccountPhoneNumberIDRequired
	}
	account, ok, err := u.repository.FindAccountByTenantID(ctx, tenantID)
	if err != nil {
		return err
	}
	if !ok || !strings.EqualFold(strings.TrimSpace(account.PhoneNumberID), phoneNumberID) {
		return ErrWhatsAppAccountNotFound
	}
	return u.repository.DeleteAccountByTenantAndPhoneNumberID(ctx, tenantID, phoneNumberID)
}
