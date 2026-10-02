package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type connectorAccounts struct {
	byTenant map[string]domain.WhatsAppAccount
}

func newConnectorAccounts() *connectorAccounts {
	return &connectorAccounts{byTenant: map[string]domain.WhatsAppAccount{}}
}

func (r *connectorAccounts) EnsureSchema(context.Context) error { return nil }

func (r *connectorAccounts) SaveAccount(_ context.Context, account domain.WhatsAppAccount) (domain.WhatsAppAccount, error) {
	r.byTenant[account.TenantID] = account
	return account, nil
}

func (r *connectorAccounts) UpdateAccountStatus(_ context.Context, account domain.WhatsAppAccount) error {
	stored := r.byTenant[account.TenantID]
	account.EncryptedAccessToken, account.AccessTokenIV = stored.EncryptedAccessToken, stored.AccessTokenIV
	r.byTenant[account.TenantID] = account
	return nil
}

func (r *connectorAccounts) SetTokenStatus(_ context.Context, tenantID, status string) error {
	account := r.byTenant[tenantID]
	account.TokenStatus = status
	r.byTenant[tenantID] = account
	return nil
}

func (r *connectorAccounts) FindAccountByTenantID(_ context.Context, tenantID string) (domain.WhatsAppAccount, bool, error) {
	account, ok := r.byTenant[tenantID]
	return account, ok, nil
}

func (r *connectorAccounts) FindAccountByPhoneNumberID(_ context.Context, phoneNumberID string) (domain.WhatsAppAccount, bool, error) {
	for _, account := range r.byTenant {
		if account.PhoneNumberID == phoneNumberID {
			return account, true, nil
		}
	}
	return domain.WhatsAppAccount{}, false, nil
}

func (r *connectorAccounts) FindAccountByBusinessAccountID(_ context.Context, businessAccountID string) (domain.WhatsAppAccount, bool, error) {
	for _, account := range r.byTenant {
		if account.BusinessAccountID == businessAccountID {
			return account, true, nil
		}
	}
	return domain.WhatsAppAccount{}, false, nil
}

func (r *connectorAccounts) ListAccounts(context.Context) ([]domain.WhatsAppAccount, error) {
	accounts := make([]domain.WhatsAppAccount, 0, len(r.byTenant))
	for _, account := range r.byTenant {
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func (r *connectorAccounts) DeleteAccountByTenantAndPhoneNumberID(_ context.Context, tenantID, _ string) error {
	delete(r.byTenant, tenantID)
	return nil
}

type connectorSignup struct {
	numbers       []domain.WhatsAppAccount
	granted       []string
	subscribeErr  error
	registerErr   error
	subscribed    bool
	registerCalls int
	lastPIN       string
	unsubscribed  bool
}

func (s *connectorSignup) ExchangeCodeForLongLivedToken(context.Context, string) (string, error) {
	return "token", nil
}

func (s *connectorSignup) InspectToken(context.Context, string) (domain.WhatsAppTokenInfo, error) {
	return domain.WhatsAppTokenInfo{Valid: true, NeverExpires: true, BusinessAccountIDs: s.granted}, nil
}

func (s *connectorSignup) ListPhoneNumbers(context.Context, string, string) ([]domain.WhatsAppAccount, error) {
	return s.numbers, nil
}

func (s *connectorSignup) GetPhoneNumberProfile(_ context.Context, _, phoneNumberID string) (domain.WhatsAppAccount, error) {
	return domain.WhatsAppAccount{PhoneNumberID: phoneNumberID, DisplayPhoneNumber: "+234 800 000 0000", QualityRating: "GREEN", NumberStatus: "CONNECTED"}, nil
}

func (s *connectorSignup) GetBusinessProfile(context.Context, string, string) (domain.WhatsAppAccount, error) {
	return domain.WhatsAppAccount{BusinessName: "Truckmoov"}, nil
}

func (s *connectorSignup) SubscribeBusinessApp(context.Context, string, string) error {
	if s.subscribeErr == nil {
		s.subscribed = true
	}
	return s.subscribeErr
}

func (s *connectorSignup) UnsubscribeBusinessApp(context.Context, string, string) error {
	s.unsubscribed = true
	return nil
}

func (s *connectorSignup) IsAppSubscribed(context.Context, string, string) (bool, error) {
	return s.subscribed, nil
}

func (s *connectorSignup) RegisterPhoneNumber(_ context.Context, _, _, pin string) error {
	s.registerCalls++
	s.lastPIN = pin
	return s.registerErr
}

func (s *connectorSignup) DeregisterPhoneNumber(context.Context, string, string) error { return nil }

type connectorCipher struct{}

func (connectorCipher) Encrypt(token string) (string, string, error) {
	return "enc:" + token, "iv", nil
}

func (connectorCipher) Decrypt(ciphertext, _ string) (string, error) {
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

func newConnectorUseCase(accounts *connectorAccounts, signup *connectorSignup) *ConnectWhatsAppAccountUseCase {
	clock := fixedNotificationClock{now: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}
	return NewConnectWhatsAppAccountUseCase(accounts, signup, connectorCipher{}, clock, &sequentialIDs{}, "system")
}

func oneNumber() []domain.WhatsAppAccount {
	return []domain.WhatsAppAccount{{PhoneNumberID: "pn-1", DisplayPhoneNumber: "+234 800 000 0000"}}
}

func TestConnectFindsTheNumberWhenOnlyTheBusinessAccountWasPosted(t *testing.T) {
	accounts, signup := newConnectorAccounts(), &connectorSignup{numbers: oneNumber()}
	account, err := newConnectorUseCase(accounts, signup).Execute(context.Background(), ConnectWhatsAppAccountCommand{
		TenantID: "system", BusinessAccountID: "waba-1", Code: "code",
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if account.PhoneNumberID != "pn-1" || account.RegisteredAt == nil || account.WebhookSubscribedAt == nil {
		t.Fatalf("expected the number found, registered and subscribed, got %+v", account)
	}
	if signup.lastPIN == "" || len(signup.lastPIN) != 6 || account.EncryptedPIN == "" {
		t.Fatalf("expected a six digit pin sent and kept encrypted, got %q", signup.lastPIN)
	}
}

func TestConnectReadsTheBusinessAccountOffTheToken(t *testing.T) {
	accounts, signup := newConnectorAccounts(), &connectorSignup{numbers: oneNumber(), granted: []string{"waba-granted"}}
	account, err := newConnectorUseCase(accounts, signup).Execute(context.Background(), ConnectWhatsAppAccountCommand{TenantID: "system", Code: "code"})
	if err != nil || account.BusinessAccountID != "waba-granted" {
		t.Fatalf("expected the granted account, got %+v, %v", account, err)
	}
}

func TestConnectRefusesABusinessAccountTheTokenDoesNotOwn(t *testing.T) {
	accounts, signup := newConnectorAccounts(), &connectorSignup{numbers: oneNumber(), granted: []string{"waba-granted"}}
	_, err := newConnectorUseCase(accounts, signup).Execute(context.Background(), ConnectWhatsAppAccountCommand{
		TenantID: "system", BusinessAccountID: "someone-elses", Code: "code",
	})
	if !errors.Is(err, ErrWhatsAppBusinessNotOwned) {
		t.Fatalf("expected the ownership refusal, got %v", err)
	}
}

func TestConnectRefusesANumberAnotherTenantHolds(t *testing.T) {
	accounts, signup := newConnectorAccounts(), &connectorSignup{numbers: oneNumber()}
	accounts.byTenant["tenant-a"] = domain.WhatsAppAccount{TenantID: "tenant-a", PhoneNumberID: "pn-1"}
	_, err := newConnectorUseCase(accounts, signup).Execute(context.Background(), ConnectWhatsAppAccountCommand{
		TenantID: "system", BusinessAccountID: "waba-1", PhoneNumberID: "pn-1", Code: "code",
	})
	if !errors.Is(err, domain.ErrWhatsAppNumberInUse) {
		t.Fatalf("expected number in use, got %v", err)
	}
}

func TestConnectKeepsWorkingWhenASubsequentStepFails(t *testing.T) {
	accounts := newConnectorAccounts()
	signup := &connectorSignup{
		numbers:      oneNumber(),
		subscribeErr: &domain.WhatsAppAPIError{HTTPStatus: 400, Code: 100, Message: "bad"},
		registerErr:  &domain.WhatsAppAPIError{HTTPStatus: 400, Code: 133005, Message: "pin"},
	}
	account, err := newConnectorUseCase(accounts, signup).Execute(context.Background(), ConnectWhatsAppAccountCommand{
		TenantID: "system", BusinessAccountID: "waba-1", Code: "code",
	})
	if err != nil {
		t.Fatalf("connect should still save: %v", err)
	}
	if account.WebhookSubscribedAt != nil || account.WebhookError == "" {
		t.Fatalf("expected a recorded subscription failure, got %+v", account)
	}
	if account.RegisteredAt != nil || !strings.Contains(account.RegisterError, "two-step PIN") {
		t.Fatalf("expected a plain registration failure, got %q", account.RegisterError)
	}
	failed := 0
	for _, step := range account.ConnectionSteps() {
		if step.Status == domain.WhatsAppStepFailed {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("expected two failed steps, got %d", failed)
	}
}

func TestRegisterReusesTheStoredPin(t *testing.T) {
	accounts, signup := newConnectorAccounts(), &connectorSignup{numbers: oneNumber(), registerErr: errors.New("down")}
	connect := newConnectorUseCase(accounts, signup)
	if _, err := connect.Execute(context.Background(), ConnectWhatsAppAccountCommand{TenantID: "system", BusinessAccountID: "waba-1", Code: "code"}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	first := signup.lastPIN
	signup.registerErr = nil
	connector := NewWhatsAppConnector(accounts, signup, connectorCipher{}, fixedNotificationClock{now: time.Now()}, "system")
	account, err := connector.Register(context.Background(), "system")
	if err != nil || account.RegisteredAt == nil {
		t.Fatalf("expected the retry to register, got %+v, %v", account, err)
	}
	if signup.lastPIN != first {
		t.Fatalf("expected the same pin %q, got %q", first, signup.lastPIN)
	}
}

func TestRefreshFlagsADeadToken(t *testing.T) {
	accounts := newConnectorAccounts()
	accounts.byTenant["system"] = domain.WhatsAppAccount{TenantID: "system", PhoneNumberID: "pn-1", BusinessAccountID: "waba-1", EncryptedAccessToken: "enc:token", TokenStatus: domain.WhatsAppTokenValid}
	signup := &deadTokenSignup{connectorSignup: &connectorSignup{}}
	connector := NewWhatsAppConnector(accounts, signup, connectorCipher{}, fixedNotificationClock{now: time.Now()}, "system")
	account, err := connector.Refresh(context.Background(), "system")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if account.TokenStatus != domain.WhatsAppTokenNeedsReconnect {
		t.Fatalf("expected reconnect required, got %q", account.TokenStatus)
	}
}

type deadTokenSignup struct{ *connectorSignup }

func (deadTokenSignup) InspectToken(context.Context, string) (domain.WhatsAppTokenInfo, error) {
	return domain.WhatsAppTokenInfo{Valid: false}, nil
}

func (deadTokenSignup) GetPhoneNumberProfile(context.Context, string, string) (domain.WhatsAppAccount, error) {
	return domain.WhatsAppAccount{}, &domain.WhatsAppAPIError{HTTPStatus: 401, Code: 190, Message: "expired"}
}

func TestDisconnectUnsubscribesAndKeepsGoingWhenMetaFails(t *testing.T) {
	accounts, signup := newConnectorAccounts(), &connectorSignup{numbers: oneNumber()}
	if _, err := newConnectorUseCase(accounts, signup).Execute(context.Background(), ConnectWhatsAppAccountCommand{TenantID: "system", BusinessAccountID: "waba-1", Code: "code"}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	connector := NewWhatsAppConnector(accounts, signup, connectorCipher{}, fixedNotificationClock{now: time.Now()}, "system")
	result, err := NewDisconnectWhatsAppAccountUseCase(accounts, connector, signup, "system").Execute(context.Background(), DisconnectWhatsAppAccountCommand{TenantID: "system", PhoneNumberID: "pn-1"})
	if err != nil || !result.Unsubscribed || !signup.unsubscribed {
		t.Fatalf("expected webhooks unsubscribed, got %+v, %v", result, err)
	}
	if _, found, _ := accounts.FindAccountByTenantID(context.Background(), "system"); found {
		t.Fatal("expected the account removed")
	}
}
