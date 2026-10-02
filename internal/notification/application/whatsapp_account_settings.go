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
	ErrWhatsAppNoPhoneNumber                = errors.New("this WhatsApp Business Account has no phone number yet; finish adding one in the signup window")
	ErrWhatsAppPhoneChoiceRequired          = errors.New("this WhatsApp Business Account has several phone numbers; choose one")
	ErrWhatsAppPhoneNotOnAccount            = errors.New("that phone number does not belong to this WhatsApp Business Account")
	ErrWhatsAppBusinessNotOwned             = errors.New("this signup did not grant access to that WhatsApp Business Account")
	ErrWhatsAppBusinessUnknown              = errors.New("Meta did not say which WhatsApp Business Account was set up; try the signup again")
)

type ConnectWhatsAppAccountCommand struct {
	TenantID          string
	PhoneNumberID     string
	BusinessAccountID string
	Code              string
	Coexistence       bool
}

type GetWhatsAppAccountQuery struct {
	TenantID string
}

type DisconnectWhatsAppAccountCommand struct {
	TenantID      string
	PhoneNumberID string
	Deregister    bool
}

type DisconnectWhatsAppAccountResult struct {
	PhoneNumberID string   `json:"phone_number_id"`
	Unsubscribed  bool     `json:"unsubscribed"`
	Deregistered  bool     `json:"deregistered"`
	Warnings      []string `json:"warnings,omitempty"`
}

type ConnectWhatsAppAccountUseCase struct {
	repository    WhatsAppAccountRepository
	signup        WhatsAppEmbeddedSignupProvider
	cipher        WhatsAppTokenCipher
	connector     *WhatsAppConnector
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
	signup        WhatsAppEmbeddedSignupProvider
	connector     *WhatsAppConnector
	defaultTenant string
}

func NewConnectWhatsAppAccountUseCase(repository WhatsAppAccountRepository, signup WhatsAppEmbeddedSignupProvider, cipher WhatsAppTokenCipher, clock Clock, idGenerator IDGenerator, defaultTenant string) *ConnectWhatsAppAccountUseCase {
	return &ConnectWhatsAppAccountUseCase{
		repository: repository, signup: signup, cipher: cipher, clock: clock, idGenerator: idGenerator, defaultTenant: defaultTenant,
		connector: NewWhatsAppConnector(repository, signup, cipher, clock, defaultTenant),
	}
}

func NewGetWhatsAppAccountUseCase(repository WhatsAppAccountRepository, defaultTenant string) *GetWhatsAppAccountUseCase {
	return &GetWhatsAppAccountUseCase{repository: repository, defaultTenant: defaultTenant}
}

func NewDisconnectWhatsAppAccountUseCase(repository WhatsAppAccountRepository, connector *WhatsAppConnector, signup WhatsAppEmbeddedSignupProvider, defaultTenant string) *DisconnectWhatsAppAccountUseCase {
	return &DisconnectWhatsAppAccountUseCase{repository: repository, connector: connector, signup: signup, defaultTenant: defaultTenant}
}

func (u *ConnectWhatsAppAccountUseCase) Execute(ctx context.Context, command ConnectWhatsAppAccountCommand) (domain.WhatsAppAccount, error) {
	tenantID, err := resolveNotificationTenant(command.TenantID, u.defaultTenant)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	code := strings.TrimSpace(command.Code)
	if code == "" {
		return domain.WhatsAppAccount{}, ErrWhatsAppAccountCodeRequired
	}
	if u.signup == nil {
		return domain.WhatsAppAccount{}, errors.New("whatsapp embedded signup is not configured")
	}
	if u.cipher == nil {
		return domain.WhatsAppAccount{}, errWhatsAppCipherMissing
	}

	accessToken, err := u.signup.ExchangeCodeForLongLivedToken(ctx, code)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	info, _ := u.signup.InspectToken(ctx, accessToken)
	businessAccountID, err := resolveBusinessAccount(command.BusinessAccountID, info)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	number, err := u.resolveNumber(ctx, accessToken, businessAccountID, command.PhoneNumberID)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	account, err := u.newAccount(ctx, tenantID, businessAccountID, accessToken, number, command.Coexistence)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	account.TokenNeverExpires, account.TokenExpiresAt = info.NeverExpires, info.ExpiresAt
	u.completeSetup(ctx, &account, accessToken)
	return u.repository.SaveAccount(ctx, account)
}

// resolveBusinessAccount trusts the token over the browser: when Meta lists
// the accounts the token was granted, the posted one must be among them, and
// when none was posted the single granted one is used.
func resolveBusinessAccount(posted string, info domain.WhatsAppTokenInfo) (string, error) {
	posted = strings.TrimSpace(posted)
	granted := info.BusinessAccountIDs
	if posted == "" {
		if len(granted) == 1 {
			return granted[0], nil
		}
		return "", ErrWhatsAppBusinessUnknown
	}
	if len(granted) == 0 {
		return posted, nil
	}
	for _, id := range granted {
		if id == posted {
			return posted, nil
		}
	}
	return "", ErrWhatsAppBusinessNotOwned
}

// resolveNumber proves the token owns the business account by reading its
// numbers from Meta, instead of trusting ids the browser posted.
func (u *ConnectWhatsAppAccountUseCase) resolveNumber(ctx context.Context, accessToken, businessAccountID, requestedID string) (domain.WhatsAppAccount, error) {
	numbers, err := u.signup.ListPhoneNumbers(ctx, accessToken, businessAccountID)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	requestedID = strings.TrimSpace(requestedID)
	for _, number := range numbers {
		if requestedID == "" || number.PhoneNumberID == requestedID {
			if requestedID == "" && len(numbers) > 1 {
				return domain.WhatsAppAccount{}, ErrWhatsAppPhoneChoiceRequired
			}
			return number, nil
		}
	}
	if requestedID == "" {
		return domain.WhatsAppAccount{}, ErrWhatsAppNoPhoneNumber
	}
	return domain.WhatsAppAccount{}, ErrWhatsAppPhoneNotOnAccount
}

func (u *ConnectWhatsAppAccountUseCase) newAccount(ctx context.Context, tenantID, businessAccountID, accessToken string, number domain.WhatsAppAccount, coexistence bool) (domain.WhatsAppAccount, error) {
	owner, taken, err := u.repository.FindAccountByPhoneNumberID(ctx, number.PhoneNumberID)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	if taken && owner.TenantID != tenantID {
		return domain.WhatsAppAccount{}, domain.ErrWhatsAppNumberInUse
	}
	ciphertext, iv, err := u.cipher.Encrypt(accessToken)
	if err != nil {
		return domain.WhatsAppAccount{}, err
	}
	now := u.clock.Now().UTC()
	account := number
	account.ID = u.idGenerator.New("wacfg")
	account.TenantID = tenantID
	account.BusinessAccountID = businessAccountID
	account.EncryptedAccessToken, account.AccessTokenIV = ciphertext, iv
	account.KeyID = cipherKeyID(u.cipher)
	account.TokenStatus = domain.WhatsAppTokenValid
	account.Coexistence = coexistence
	account.ConnectedAt, account.CreatedAt, account.UpdatedAt = now, now, now
	if taken {
		account.EncryptedPIN, account.PINIV = owner.EncryptedPIN, owner.PINIV
	}
	return account, nil
}

// completeSetup runs the steps after the token is stored. A failing step is
// recorded on the account and the rest still run, so one Meta hiccup does not
// hide what worked.
func (u *ConnectWhatsAppAccountUseCase) completeSetup(ctx context.Context, account *domain.WhatsAppAccount, accessToken string) {
	if business, err := u.signup.GetBusinessProfile(ctx, accessToken, account.BusinessAccountID); err == nil {
		account.BusinessName = strings.TrimSpace(business.BusinessName)
	}
	_ = u.connector.subscribe(ctx, account, accessToken)
	if !account.Coexistence {
		_ = u.connector.register(ctx, account, accessToken)
	}
	u.connector.checkProfile(ctx, account, accessToken)
	now := u.clock.Now().UTC()
	account.LastCheckedAt = &now
}

func cipherKeyID(cipher WhatsAppTokenCipher) string {
	if identified, ok := cipher.(interface{ KeyID() string }); ok {
		return identified.KeyID()
	}
	return ""
}

func (u *GetWhatsAppAccountUseCase) Execute(ctx context.Context, query GetWhatsAppAccountQuery) (domain.WhatsAppAccount, bool, error) {
	tenantID, err := resolveNotificationTenant(query.TenantID, u.defaultTenant)
	if err != nil {
		return domain.WhatsAppAccount{}, false, err
	}
	return u.repository.FindAccountByTenantID(ctx, tenantID)
}

func (u *DisconnectWhatsAppAccountUseCase) Execute(ctx context.Context, command DisconnectWhatsAppAccountCommand) (DisconnectWhatsAppAccountResult, error) {
	tenantID, err := resolveNotificationTenant(command.TenantID, u.defaultTenant)
	if err != nil {
		return DisconnectWhatsAppAccountResult{}, err
	}
	phoneNumberID := strings.TrimSpace(command.PhoneNumberID)
	if phoneNumberID == "" {
		return DisconnectWhatsAppAccountResult{}, ErrWhatsAppAccountPhoneNumberIDRequired
	}
	account, ok, err := u.repository.FindAccountByTenantID(ctx, tenantID)
	if err != nil {
		return DisconnectWhatsAppAccountResult{}, err
	}
	if !ok || !strings.EqualFold(strings.TrimSpace(account.PhoneNumberID), phoneNumberID) {
		return DisconnectWhatsAppAccountResult{}, ErrWhatsAppAccountNotFound
	}
	result := u.releaseAtMeta(ctx, account, command.Deregister)
	result.PhoneNumberID = phoneNumberID
	return result, u.repository.DeleteAccountByTenantAndPhoneNumberID(ctx, tenantID, phoneNumberID)
}

// releaseAtMeta is best effort: a dead token must not stop staff removing the
// account, so each failure becomes a warning instead.
func (u *DisconnectWhatsAppAccountUseCase) releaseAtMeta(ctx context.Context, account domain.WhatsAppAccount, deregister bool) DisconnectWhatsAppAccountResult {
	var result DisconnectWhatsAppAccountResult
	token, err := u.connector.decryptToken(account)
	if err != nil {
		result.Warnings = append(result.Warnings, "Meta was not told about the disconnect: "+err.Error())
		return result
	}
	if u.businessAccountShared(ctx, account) {
		result.Warnings = append(result.Warnings, "Another account uses this WhatsApp Business Account, so webhooks stay subscribed.")
	} else if err := u.signup.UnsubscribeBusinessApp(ctx, token, account.BusinessAccountID); err != nil {
		result.Warnings = append(result.Warnings, "Webhooks could not be unsubscribed: "+domain.PlainErrorText(err))
	} else {
		result.Unsubscribed = true
	}
	if deregister {
		if err := u.signup.DeregisterPhoneNumber(ctx, token, account.PhoneNumberID); err != nil {
			result.Warnings = append(result.Warnings, "The number could not be deregistered: "+domain.PlainErrorText(err))
		} else {
			result.Deregistered = true
		}
	}
	return result
}

func (u *DisconnectWhatsAppAccountUseCase) businessAccountShared(ctx context.Context, account domain.WhatsAppAccount) bool {
	accounts, err := u.repository.ListAccounts(ctx)
	if err != nil {
		return true
	}
	for _, other := range accounts {
		if other.TenantID != account.TenantID && other.BusinessAccountID == account.BusinessAccountID {
			return true
		}
	}
	return false
}
