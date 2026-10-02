package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeTemplateProviderAccountRepository struct {
	account domain.WhatsAppAccount
	found   bool
	err     error
}

func (f *fakeTemplateProviderAccountRepository) FindAccountByTenantID(_ context.Context, tenantID string) (domain.WhatsAppAccount, bool, error) {
	if f.err != nil {
		return domain.WhatsAppAccount{}, false, f.err
	}
	if !f.found || tenantID != f.account.TenantID {
		return domain.WhatsAppAccount{}, false, nil
	}
	return f.account, true, nil
}

type fakeTemplateProviderTokenCipher struct{}

func (f *fakeTemplateProviderTokenCipher) Decrypt(ciphertext, _ string) (string, error) {
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

func TestMetaWhatsAppTemplateProviderCreateTemplate_Success(t *testing.T) {
	var gotPath string
	var gotAuthorization string
	var gotPayload domain.WhatsAppTemplateCreateRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"tpl-1","status":"PENDING","category":"UTILITY"}`))
	}))
	defer server.Close()

	provider := NewMetaWhatsAppTemplateProviderWithClient(server.URL, nil, nil, "waba-123", "access-token", server.Client())
	response, err := provider.CreateTemplate(context.Background(), domain.WhatsAppTemplateCreateRequest{
		TenantID: "tenant-a",
		Name:     "job_instruction_v1",
		Language: "en_US",
		Category: "UTILITY",
		Components: []map[string]any{{
			"type": "BODY",
			"text": "Hello {{1}}",
		}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if gotPath != "/waba-123/message_templates" {
		t.Fatalf("expected path /waba-123/message_templates, got %s", gotPath)
	}
	if gotAuthorization != "Bearer access-token" {
		t.Fatalf("expected auth header to use bearer token, got %q", gotAuthorization)
	}
	if response.ID != "tpl-1" {
		t.Fatalf("expected template id tpl-1, got %q", response.ID)
	}
	if gotPayload.Language != "en_US" {
		t.Fatalf("expected language en_US, got %q", gotPayload.Language)
	}
}

func TestMetaWhatsAppTemplateProviderListTemplates_Success(t *testing.T) {
	var gotRawQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"tpl-1","name":"job_instruction_v1","language":"en_US","status":"APPROVED","category":"UTILITY","components":[{"type":"BODY","text":"Hello {{1}}"}]}],"paging":{"cursors":{"before":"before-1","after":"after-1"},"next":"https://graph.facebook.com/next"}}`))
	}))
	defer server.Close()

	provider := NewMetaWhatsAppTemplateProviderWithClient(server.URL, nil, nil, "waba-123", "access-token", server.Client())
	response, err := provider.ListTemplates(context.Background(), domain.WhatsAppTemplateListRequest{Limit: 25, After: "cursor-1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(gotRawQuery, "after=cursor-1") || !strings.Contains(gotRawQuery, "limit=25") {
		t.Fatalf("expected query to contain after and limit, got %q", gotRawQuery)
	}
	if len(response.Data) != 1 {
		t.Fatalf("expected 1 template, got %d", len(response.Data))
	}
	if response.Data[0].Status != "APPROVED" {
		t.Fatalf("expected APPROVED template, got %q", response.Data[0].Status)
	}
	if response.Paging == nil || response.Paging.Cursors["after"] != "after-1" {
		t.Fatalf("expected paging cursor after-1, got %#v", response.Paging)
	}
}

func TestMetaWhatsAppTemplateProviderListTemplates_UsesConnectedTenantAccount(t *testing.T) {
	var gotPath string
	var gotAuthorization string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	provider := NewMetaWhatsAppTemplateProviderWithClient(server.URL, &fakeTemplateProviderAccountRepository{
		found: true,
		account: domain.WhatsAppAccount{
			TenantID:             "tenant-db",
			BusinessAccountID:    "waba-db",
			EncryptedAccessToken: "enc:db-token",
			AccessTokenIV:        "iv",
		},
	}, &fakeTemplateProviderTokenCipher{}, "legacy-waba", "legacy-token", server.Client())

	_, err := provider.ListTemplates(context.Background(), domain.WhatsAppTemplateListRequest{TenantID: "tenant-db", Limit: 10})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if gotAuthorization != "Bearer db-token" {
		t.Fatalf("expected db-backed auth header, got %q", gotAuthorization)
	}
	if gotPath != "/waba-db/message_templates" {
		t.Fatalf("expected db-backed business account path, got %s", gotPath)
	}
}
