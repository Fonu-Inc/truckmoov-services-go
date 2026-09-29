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

type dotgoRequest struct {
	method      string
	path        string
	auth        string
	contentType string
	payload     map[string]any
}

func captureDotgo(t *testing.T, status int, response string) (*httptest.Server, *dotgoRequest, *int) {
	t.Helper()

	got := &dotgoRequest{}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		got.method = r.Method
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		got.contentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got.payload)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server, got, &calls
}

func TestDotgoSMSMessageProviderSend_PostsToTheAccountMessagesEndpoint(t *testing.T) {
	server, got, _ := captureDotgo(t, http.StatusOK, `{"id":"abc"}`)

	provider := NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "secret-token", "Truckmoov", "https://hooks.example/sms", server.Client())
	status, err := provider.Send(context.Background(), "tenant-a", "sms", "+2348012345678", "Job assigned")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", status)
	}
	if got.method != http.MethodPost {
		t.Fatalf("expected POST, got %s", got.method)
	}
	if got.path != "/api/v1/Accounts/acc-1/Messages" {
		t.Fatalf("expected the account Messages path, got %s", got.path)
	}
	if got.auth != "secret-token" {
		t.Fatalf("expected the raw account token in Authorization, got %q", got.auth)
	}
	if got.contentType != "application/json" {
		t.Fatalf("expected a JSON body, got %q", got.contentType)
	}

	if id, _ := got.payload["id"].(string); id == "" {
		t.Fatalf("expected a request id, got %v", got.payload["id"])
	}
	recipients, _ := got.payload["to"].([]any)
	if len(recipients) != 1 || recipients[0] != "2348012345678" {
		t.Fatalf("expected recipients [2348012345678], got %v", got.payload["to"])
	}
	if got.payload["body"] != "Job assigned" {
		t.Fatalf("unexpected body %v", got.payload["body"])
	}
	if got.payload["sender_mask"] != "Truckmoov" {
		t.Fatalf("unexpected sender_mask %v", got.payload["sender_mask"])
	}
	if got.payload["callback_url"] != "https://hooks.example/sms" {
		t.Fatalf("unexpected callback_url %v", got.payload["callback_url"])
	}
}

func TestDotgoSMSMessageProviderSend_OmitsCallbackWhenUnset(t *testing.T) {
	server, got, _ := captureDotgo(t, http.StatusOK, `{}`)

	provider := NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "tok", "Truckmoov", "", server.Client())
	if _, err := provider.Send(context.Background(), "tenant-a", "sms", "08012345678", "Hi"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, present := got.payload["callback_url"]; present {
		t.Fatalf("expected no callback_url, got %v", got.payload["callback_url"])
	}
}

func TestDotgoSMSMessageProviderSend_UsesAFreshRequestIDEachTime(t *testing.T) {
	server, got, _ := captureDotgo(t, http.StatusOK, `{}`)
	provider := NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "tok", "Truckmoov", "", server.Client())

	_, _ = provider.Send(context.Background(), "t", "sms", "08012345678", "one")
	first := got.payload["id"]
	_, _ = provider.Send(context.Background(), "t", "sms", "08012345678", "two")

	if first == got.payload["id"] {
		t.Fatalf("expected a new id per message, got %v twice", first)
	}
}

func TestDotgoSMSMessageProviderSend_ReportsAnErrorResponse(t *testing.T) {
	server, _, _ := captureDotgo(t, http.StatusUnauthorized, `{"message":"invalid token"}`)

	provider := NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "secret-token", "Truckmoov", "", server.Client())
	status, err := provider.Send(context.Background(), "tenant-a", "sms", "+2348012345678", "Job assigned")
	if err == nil {
		t.Fatal("expected dotgo failure error")
	}
	if status != domain.StatusFailed {
		t.Fatalf("expected failed status, got %s", status)
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("expected status and body in the error, got %v", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("the account token must not appear in the error, got %v", err)
	}
}

func TestDotgoSMSMessageProviderSend_FailsWithoutCallingWhenUnconfigured(t *testing.T) {
	server, _, calls := captureDotgo(t, http.StatusOK, `{}`)

	for name, provider := range map[string]*DotgoSMSMessageProvider{
		"no base url":   NewDotgoSMSMessageProviderWithClient("", "acc-1", "tok", "Truckmoov", "", server.Client()),
		"no account id": NewDotgoSMSMessageProviderWithClient(server.URL, "", "tok", "Truckmoov", "", server.Client()),
		"no token":      NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "", "Truckmoov", "", server.Client()),
		"no sender":     NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "tok", "", "", server.Client()),
	} {
		status, err := provider.Send(context.Background(), "tenant-a", "sms", "+2348012345678", "Hi")
		if err == nil || status != domain.StatusFailed {
			t.Fatalf("%s: expected a configuration failure, got status %s err %v", name, status, err)
		}
	}
	if *calls != 0 {
		t.Fatalf("expected no requests to Dotgo, got %d", *calls)
	}
}

func TestDotgoSMSMessageProviderSend_RejectsUnsupportedChannel(t *testing.T) {
	provider := NewDotgoSMSMessageProvider("https://dotgo.example", "acc-1", "tok", "Truckmoov", "")

	status, err := provider.Send(context.Background(), "tenant-a", "whatsapp", "+2348012345678", "Hi")
	if err == nil || status != domain.StatusFailed {
		t.Fatalf("expected an unsupported channel failure, got status %s err %v", status, err)
	}
}

func TestNormalizeDotgoRecipient(t *testing.T) {
	cases := map[string]string{
		"+2348012345678":     "2348012345678",
		"2348012345678":      "2348012345678",
		"08012345678":        "2348012345678",
		"8012345678":         "2348012345678",
		" +234 801-234 5678": "2348012345678",
		"(0801) 234 5678":    "2348012345678",
		"+14155550123":       "14155550123",
	}
	for input, want := range cases {
		if got := normalizeDotgoRecipient(input); got != want {
			t.Errorf("normalizeDotgoRecipient(%q) = %q, want %q", input, got, want)
		}
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
		NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "tok", "Truckmoov", "", server.Client()),
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

func TestValidDotgoCallbackURL(t *testing.T) {
	cases := map[string]bool{
		"https://api.example.com/api/v1/external-integrations/sms/webhook": true,
		"http://localhost:8080/hook":                                       true,
		"":                                                                 false,
		"   ":                                                              false,
		" <fonu-api-base-url >/v1/external-integrations/sms/webhook": false,
		"<fonu-api-base-url>/v1/external-integrations/sms/webhook":   false,
		"/v1/external-integrations/sms/webhook":                      false,
		"ftp://example.com/hook":                                     false,
		"https://example.com/has space":                              false,
		"not a url":                                                  false,
	}
	for input, want := range cases {
		if got := ValidDotgoCallbackURL(input); got != want {
			t.Errorf("ValidDotgoCallbackURL(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestNewDotgoRequestID_IsTwelveDigits(t *testing.T) {
	for i := 0; i < 50; i++ {
		id, err := newDotgoRequestID()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(id) != 12 {
			t.Fatalf("expected 12 characters, got %q", id)
		}
		for _, r := range id {
			if r < '0' || r > '9' {
				t.Fatalf("expected digits only, got %q", id)
			}
		}
	}
}

func TestDotgoCallbackURLWithToken(t *testing.T) {
	cases := []struct {
		name, url, secret, want string
	}{
		{"adds the token", "https://api.example.com/notify/sms/webhook", "s3cret", "https://api.example.com/notify/sms/webhook?token=s3cret"},
		{"keeps an existing query", "https://api.example.com/hook?env=dev", "s3cret", "https://api.example.com/hook?env=dev&token=s3cret"},
		{"encodes the token", "https://api.example.com/hook", "a b&c", "https://api.example.com/hook?token=a+b%26c"},
		{"no secret means no receipts", "https://api.example.com/hook", "", ""},
		{"blank secret means no receipts", "https://api.example.com/hook", "   ", ""},
		{"an unfilled template is refused", "<fonu-api-base-url>/v1/hook", "s3cret", ""},
		{"a relative path is refused", "/notify/sms/webhook", "s3cret", ""},
		{"empty is refused", "", "s3cret", ""},
	}
	for _, c := range cases {
		if got := DotgoCallbackURLWithToken(c.url, c.secret); got != c.want {
			t.Errorf("%s: DotgoCallbackURLWithToken(%q, %q) = %q, want %q", c.name, c.url, c.secret, got, c.want)
		}
	}
}

func TestDotgoSendTrackedReturnsTheRequestID(t *testing.T) {
	server, got, _ := captureDotgo(t, http.StatusOK, `{}`)
	provider := NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "tok", "Truckmoov", "", server.Client())

	result, err := provider.SendTracked(context.Background(), "tenant-a", "sms", "08012345678", "Hi")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != domain.StatusSent || result.ProviderMessageID == "" {
		t.Fatalf("expected sent with an id, got %+v", result)
	}
	if result.ProviderMessageID != got.payload["id"] {
		t.Fatalf("the returned id %q must be the one sent to Dotgo, %v", result.ProviderMessageID, got.payload["id"])
	}
}

func TestMultiChannelSendTrackedPassesTheSMSRequestIDThrough(t *testing.T) {
	server, got, _ := captureDotgo(t, http.StatusOK, `{}`)
	provider := NewMultiChannelMessageProvider(nil, NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "tok", "Truckmoov", "", server.Client()), nil)

	result, err := provider.SendTracked(context.Background(), "tenant-a", "sms", "08012345678", "Hi")

	if err != nil || result.ProviderMessageID == "" || result.ProviderMessageID != got.payload["id"] {
		t.Fatalf("expected the SMS request id through the router, got %+v err=%v", result, err)
	}
}

func TestDotgoSendTrackedReturnsNoIDOnFailure(t *testing.T) {
	server, _, _ := captureDotgo(t, http.StatusUnauthorized, `{"message":"invalid token"}`)
	provider := NewDotgoSMSMessageProviderWithClient(server.URL, "acc-1", "tok", "Truckmoov", "", server.Client())

	result, err := provider.SendTracked(context.Background(), "tenant-a", "sms", "08012345678", "Hi")

	if err == nil || result.Status != domain.StatusFailed || result.ProviderMessageID != "" {
		t.Fatalf("expected a failure with no id, got %+v err=%v", result, err)
	}
}
