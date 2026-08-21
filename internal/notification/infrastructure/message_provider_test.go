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

type fakeMessageProviderAccountRepository struct {
	account domain.WhatsAppAccount
	found   bool
	err     error
}

func (f *fakeMessageProviderAccountRepository) FindAccountByTenantID(_ context.Context, tenantID string) (domain.WhatsAppAccount, bool, error) {
	if f.err != nil {
		return domain.WhatsAppAccount{}, false, f.err
	}
	if !f.found || strings.TrimSpace(f.account.TenantID) != strings.TrimSpace(tenantID) {
		return domain.WhatsAppAccount{}, false, nil
	}
	return f.account, true, nil
}

type fakeMessageProviderTokenCipher struct{}

func (f *fakeMessageProviderTokenCipher) Decrypt(ciphertext, _ string) (string, error) {
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

func TestMetaWhatsAppMessageProviderSend_Success(t *testing.T) {
	t.Helper()

	var gotAuthorization string
	var gotPath string
	var gotPayload struct {
		MessagingProduct string `json:"messaging_product"`
		To               string `json:"to"`
		Text             struct {
			Body string `json:"body"`
		} `json:"text"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.test"}]}`))
	}))
	defer server.Close()

	provider := NewMetaWhatsAppMessageProviderWithClient(server.URL, nil, nil, "123456", "access-token", server.Client())

	status, err := provider.Send(context.Background(), "tenant-a", "whatsapp", "whatsapp:+234 801 234 5678", "job assigned")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", status)
	}
	if gotAuthorization != "Bearer access-token" {
		t.Fatalf("expected auth header to use bearer token, got %q", gotAuthorization)
	}
	if gotPath != "/123456/messages" {
		t.Fatalf("expected path /123456/messages, got %s", gotPath)
	}
	if gotPayload.MessagingProduct != "whatsapp" {
		t.Fatalf("expected whatsapp messaging product, got %s", gotPayload.MessagingProduct)
	}
	if gotPayload.To != "2348012345678" {
		t.Fatalf("expected normalized recipient, got %s", gotPayload.To)
	}
	if gotPayload.Text.Body != "job assigned" {
		t.Fatalf("expected request body text to match content, got %q", gotPayload.Text.Body)
	}
}

func TestMetaWhatsAppMessageProviderSendTemplate_Success(t *testing.T) {
	t.Helper()

	var gotAuthorization string
	var gotPath string
	var gotPayload struct {
		MessagingProduct string `json:"messaging_product"`
		To               string `json:"to"`
		Type             string `json:"type"`
		Template         struct {
			Name       string            `json:"name"`
			Language   map[string]string `json:"language"`
			Components []map[string]any  `json:"components"`
		} `json:"template"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.template"}]}`))
	}))
	defer server.Close()

	provider := NewMetaWhatsAppMessageProviderWithClient(server.URL, nil, nil, "123456", "access-token", server.Client())

	result, err := provider.SendTemplate(context.Background(), domain.WhatsAppTemplateMessageRequest{
		TenantID:  "tenant-a",
		Recipient: "+2348012345678",
		Name:      "job_assignment_v1",
		Language:  "en_US",
		Components: []map[string]any{{
			"type": "body",
			"parameters": []map[string]any{{
				"type": "text",
				"text": "TM-2847",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", result.Status)
	}
	if result.MessageID != "wamid.template" {
		t.Fatalf("expected message id wamid.template, got %q", result.MessageID)
	}
	if gotAuthorization != "Bearer access-token" {
		t.Fatalf("expected auth header to use bearer token, got %q", gotAuthorization)
	}
	if gotPath != "/123456/messages" {
		t.Fatalf("expected path /123456/messages, got %s", gotPath)
	}
	if gotPayload.Type != "template" {
		t.Fatalf("expected template message type, got %q", gotPayload.Type)
	}
	if gotPayload.Template.Name != "job_assignment_v1" {
		t.Fatalf("expected template name job_assignment_v1, got %q", gotPayload.Template.Name)
	}
	if gotPayload.Template.Language["code"] != "en_US" {
		t.Fatalf("expected template language en_US, got %#v", gotPayload.Template.Language)
	}
	if len(gotPayload.Template.Components) != 1 {
		t.Fatalf("expected one template component, got %d", len(gotPayload.Template.Components))
	}
	if gotPayload.To != "2348012345678" {
		t.Fatalf("expected normalized recipient, got %s", gotPayload.To)
	}
}

func TestMetaWhatsAppMessageProviderSendMedia_Success(t *testing.T) {
	t.Helper()

	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.media"}]}`))
	}))
	defer server.Close()

	provider := NewMetaWhatsAppMessageProviderWithClient(server.URL, nil, nil, "123456", "access-token", server.Client())
	result, err := provider.SendMedia(context.Background(), domain.WhatsAppMediaMessageRequest{
		TenantID:  "tenant-a",
		Recipient: "+2348012345678",
		MediaType: "image",
		MediaLink: "https://example.com/job.png",
		Caption:   "Pickup map",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", result.Status)
	}
	if result.MessageID != "wamid.media" {
		t.Fatalf("expected media message id, got %q", result.MessageID)
	}
	if gotPayload["type"] != "image" {
		t.Fatalf("expected image payload, got %#v", gotPayload)
	}
}

func TestMetaWhatsAppMessageProviderSend_RequiresConfiguration(t *testing.T) {
	t.Helper()

	provider := NewMetaWhatsAppMessageProvider("", nil, nil, "", "")

	status, err := provider.Send(context.Background(), "tenant-a", "whatsapp", "+2348012345678", "job assigned")
	if err == nil {
		t.Fatal("expected configuration error")
	}
	if status != domain.StatusFailed {
		t.Fatalf("expected failed status, got %s", status)
	}
}

func TestMetaWhatsAppMessageProviderSend_RejectsUnsupportedChannel(t *testing.T) {
	t.Helper()

	provider := NewMetaWhatsAppMessageProvider("https://graph.facebook.com/v18.0", nil, nil, "123456", "access-token")

	status, err := provider.Send(context.Background(), "tenant-a", "sms", "+2348012345678", "job assigned")
	if err == nil {
		t.Fatal("expected unsupported channel error")
	}
	if status != domain.StatusFailed {
		t.Fatalf("expected failed status, got %s", status)
	}
}

func TestDotgoSMSMessageProviderSend_Success(t *testing.T) {
	t.Helper()

	var gotPath string
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK-123"))
	}))
	defer server.Close()

	provider := NewDotgoSMSMessageProviderWithClient(server.URL, "user", "pass", "Truckmoov", server.Client())
	status, err := provider.Send(context.Background(), "tenant-a", "sms", "+2348012345678", "Job assigned")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", status)
	}
	if gotPath != "/httpsms.php" {
		t.Fatalf("expected Dotgo path /httpsms.php, got %s", gotPath)
	}
	for _, fragment := range []string{"u=user", "p=pass", "r=%2B2348012345678", "s=Truckmoov", "m=Job+assigned", "t=1"} {
		if strings.Contains(gotQuery, fragment) == false {
			t.Fatalf("expected query to contain %q, got %q", fragment, gotQuery)
		}
	}
}

func TestDotgoSMSMessageProviderSend_MapsFailureCode(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("41"))
	}))
	defer server.Close()

	provider := NewDotgoSMSMessageProviderWithClient(server.URL, "user", "pass", "Truckmoov", server.Client())
	status, err := provider.Send(context.Background(), "tenant-a", "sms", "+2348012345678", "Job assigned")
	if err == nil {
		t.Fatal("expected dotgo failure error")
	}
	if status != domain.StatusFailed {
		t.Fatalf("expected failed status, got %s", status)
	}
	if strings.Contains(err.Error(), "INSUFFICIENT CREDIT") == false {
		t.Fatalf("expected insufficient credit error, got %v", err)
	}
}

func TestMultiChannelMessageProviderRoutesSMS(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("accepted"))
	}))
	defer server.Close()

	provider := NewMultiChannelMessageProvider(
		NewMetaWhatsAppMessageProvider(server.URL, nil, nil, "123456", "access-token"),
		NewDotgoSMSMessageProviderWithClient(server.URL, "user", "pass", "Truckmoov", server.Client()),
		nil,
	)
	status, err := provider.Send(context.Background(), "tenant-a", "sms", "+2348012345678", "Job assigned")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", status)
	}
}

func TestMetaWhatsAppMessageProviderSend_UsesConnectedTenantAccount(t *testing.T) {
	t.Helper()

	var gotAuthorization string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.db-backed"}]}`))
	}))
	defer server.Close()

	provider := NewMetaWhatsAppMessageProviderWithClient(server.URL, &fakeMessageProviderAccountRepository{
		found: true,
		account: domain.WhatsAppAccount{
			TenantID:             "tenant-db",
			PhoneNumberID:        "pnid-db",
			EncryptedAccessToken: "enc:db-token",
			AccessTokenIV:        "iv",
		},
	}, &fakeMessageProviderTokenCipher{}, "legacy-phone", "legacy-token", server.Client())

	status, err := provider.Send(context.Background(), "tenant-db", "whatsapp", "+2348012345678", "job assigned")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", status)
	}
	if gotAuthorization != "Bearer db-token" {
		t.Fatalf("expected db-backed auth header, got %q", gotAuthorization)
	}
	if gotPath != "/pnid-db/messages" {
		t.Fatalf("expected db-backed phone number id path, got %s", gotPath)
	}
}
