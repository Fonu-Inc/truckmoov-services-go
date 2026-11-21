package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

func TestAuthSMSMessageProviderPostsTheUnifiedPayload(t *testing.T) {
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth/internal/otp/send" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get(identity.ServiceTokenHeader) == "" {
			t.Fatal("missing service token")
		}
		if r.Header.Get(identity.ActingForTenantHeader) != "tenant-a" {
			t.Fatalf("unexpected tenant header %q", r.Header.Get(identity.ActingForTenantHeader))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"message":"success","data":{"success":true,"channel":"sms","message":"SMS accepted for delivery","reference":"ref-123"}}`))
	}))
	defer server.Close()

	caller := identity.NewCaller("notification", identity.NewServiceTokens("notification", t.TempDir(), "dev-secret"), "")
	provider := NewAuthSMSMessageProvider(server.URL, caller, server.Client())

	result, err := provider.SendTracked(context.Background(), "tenant-a", "sms", "+2347043863010", "hello")
	if err != nil {
		t.Fatalf("SendTracked: %v", err)
	}
	if result.Status != domain.StatusSent || result.ProviderMessageID != "ref-123" {
		t.Fatalf("unexpected result %+v", result)
	}
	if got["channel"] != "sms" || got["to"] != "+2347043863010" || got["message"] != "hello" {
		t.Fatalf("unexpected payload %+v", got)
	}
}

func TestAuthSMSMessageProviderRejectsOtherChannels(t *testing.T) {
	caller := identity.NewCaller("notification", identity.NewServiceTokens("notification", t.TempDir(), "dev-secret"), "")
	provider := NewAuthSMSMessageProvider("http://auth-service:3001", caller, nil)

	if _, err := provider.SendTracked(context.Background(), "tenant-a", "whatsapp", "+2347043863010", "hello"); err == nil {
		t.Fatal("expected an unsupported-channel error")
	}
}

func TestAuthSMSMessageProviderReportsAnErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"SMS is not configured"}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	caller := identity.NewCaller("notification", identity.NewServiceTokens("notification", t.TempDir(), "dev-secret"), "")
	provider := NewAuthSMSMessageProvider(server.URL, caller, server.Client())

	if _, err := provider.SendTracked(context.Background(), "tenant-a", "sms", "+2347043863010", "hello"); err == nil {
		t.Fatal("expected an error for a 503 response")
	}
}
