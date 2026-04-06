package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

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

	provider := NewMetaWhatsAppTemplateProviderWithClient(server.URL, "waba-123", "access-token", server.Client())
	response, err := provider.CreateTemplate(context.Background(), domain.WhatsAppTemplateCreateRequest{
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

	provider := NewMetaWhatsAppTemplateProviderWithClient(server.URL, "waba-123", "access-token", server.Client())
	response, err := provider.ListTemplates(context.Background(), domain.WhatsAppTemplateListRequest{Limit: 25, After: "cursor-1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if gotRawQuery != "after=cursor-1&limit=25" && gotRawQuery != "limit=25&after=cursor-1" {
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
