package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// A driver who hauls for two carriers has a conversation under each tenant.
// An inbound message that cannot be tied to a phone number id used to be
// attributed to whichever tenant spoke to that driver last, so one tenant read
// the other's replies.
func TestResolveWebhookConversationDoesNotGuessTheTenant(t *testing.T) {
	repository := newFakeWhatsAppConversationRepository()
	ctx := context.Background()
	driverPhone := "2348000000001"

	for index, tenantID := range []string{"tenant-a", "tenant-b"} {
		saved, err := repository.SaveConversation(ctx, domain.WhatsAppConversation{
			ID:          "conv-" + tenantID,
			TenantID:    tenantID,
			DriverPhone: driverPhone,
			CreatedAt:   time.Unix(int64(1_700_000_000+index), 0).UTC(),
			UpdatedAt:   time.Unix(int64(1_700_000_000+index), 0).UTC(),
		})
		if err != nil {
			t.Fatalf("save conversation for %s: %v", tenantID, err)
		}
		if saved.TenantID != tenantID {
			t.Fatalf("expected tenant %s, got %s", tenantID, saved.TenantID)
		}
	}

	message := whatsAppWebhookMessage{ID: "wamid.1", From: driverPhone, Type: "text"}

	// No phone number id, so nothing ties the message to a tenant.
	conversation, ok, err := resolveWebhookConversation(ctx, repository, nil, "", message)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ok {
		t.Fatalf("expected no conversation, got one for tenant %q", conversation.TenantID)
	}
}

// The supported path still works: a phone number id that maps to an account
// resolves within that account's tenant and nowhere else.
func TestResolveWebhookConversationUsesThePhoneNumberOwner(t *testing.T) {
	repository := newFakeWhatsAppConversationRepository()
	ctx := context.Background()
	driverPhone := "2348000000001"

	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		if _, err := repository.SaveConversation(ctx, domain.WhatsAppConversation{
			ID:          "conv-" + tenantID,
			TenantID:    tenantID,
			DriverPhone: driverPhone,
			CreatedAt:   time.Unix(1_700_000_000, 0).UTC(),
			UpdatedAt:   time.Unix(1_700_000_000, 0).UTC(),
		}); err != nil {
			t.Fatalf("save conversation for %s: %v", tenantID, err)
		}
	}

	accounts := &fixedWhatsAppAccountRepository{
		account: domain.WhatsAppAccount{TenantID: "tenant-b", PhoneNumberID: "pn-b"},
	}
	message := whatsAppWebhookMessage{ID: "wamid.2", From: driverPhone, Type: "text"}

	conversation, ok, err := resolveWebhookConversation(ctx, repository, accounts, "pn-b", message)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !ok {
		t.Fatal("expected a conversation")
	}
	if conversation.TenantID != "tenant-b" {
		t.Fatalf("expected tenant-b, got %s", conversation.TenantID)
	}
}

type fixedWhatsAppAccountRepository struct {
	account domain.WhatsAppAccount
}

func (r *fixedWhatsAppAccountRepository) EnsureSchema(context.Context) error { return nil }

func (r *fixedWhatsAppAccountRepository) SaveAccount(_ context.Context, account domain.WhatsAppAccount) (domain.WhatsAppAccount, error) {
	return account, nil
}

func (r *fixedWhatsAppAccountRepository) FindAccountByTenantID(_ context.Context, tenantID string) (domain.WhatsAppAccount, bool, error) {
	if r.account.TenantID != tenantID {
		return domain.WhatsAppAccount{}, false, nil
	}
	return r.account, true, nil
}

func (r *fixedWhatsAppAccountRepository) FindAccountByPhoneNumberID(_ context.Context, phoneNumberID string) (domain.WhatsAppAccount, bool, error) {
	if r.account.PhoneNumberID != phoneNumberID {
		return domain.WhatsAppAccount{}, false, nil
	}
	return r.account, true, nil
}

func (r *fixedWhatsAppAccountRepository) DeleteAccountByTenantAndPhoneNumberID(context.Context, string, string) error {
	return nil
}

func (r *fixedWhatsAppAccountRepository) UpdateAccountStatus(context.Context, domain.WhatsAppAccount) error {
	return nil
}

func (r *fixedWhatsAppAccountRepository) SetTokenStatus(context.Context, string, string) error {
	return nil
}

func (r *fixedWhatsAppAccountRepository) FindAccountByBusinessAccountID(_ context.Context, businessAccountID string) (domain.WhatsAppAccount, bool, error) {
	if r.account.BusinessAccountID != businessAccountID {
		return domain.WhatsAppAccount{}, false, nil
	}
	return r.account, true, nil
}

func (r *fixedWhatsAppAccountRepository) ListAccounts(context.Context) ([]domain.WhatsAppAccount, error) {
	return []domain.WhatsAppAccount{r.account}, nil
}
