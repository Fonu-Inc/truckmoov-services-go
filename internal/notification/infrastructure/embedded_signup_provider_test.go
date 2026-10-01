package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func signupGraph(t *testing.T, expiresAt string, exchanges *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/access_token":
			if r.URL.Query().Get("grant_type") == "fb_exchange_token" {
				*exchanges++
				_, _ = w.Write([]byte(`{"access_token":"long-lived"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"from-code"}`))
		case "/debug_token":
			_, _ = w.Write([]byte(`{"data":{"is_valid":true,"expires_at":` + expiresAt + `}}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSystemUserTokenIsKeptAsIs(t *testing.T) {
	exchanges := 0
	server := signupGraph(t, "0", &exchanges)
	defer server.Close()

	token, err := NewMetaWhatsAppEmbeddedSignupProvider(server.URL, "app", "secret").ExchangeCodeForLongLivedToken(context.Background(), "code")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if token != "from-code" || exchanges != 0 {
		t.Fatalf("expected the permanent token kept without a second exchange, got %q after %d exchanges", token, exchanges)
	}
}

func TestExpiringTokenIsExchangedForALongLivedOne(t *testing.T) {
	exchanges := 0
	server := signupGraph(t, "1893456000", &exchanges)
	defer server.Close()

	token, err := NewMetaWhatsAppEmbeddedSignupProvider(server.URL, "app", "secret").ExchangeCodeForLongLivedToken(context.Background(), "code")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if token != "long-lived" || exchanges != 1 {
		t.Fatalf("expected the long-lived token after one exchange, got %q after %d", token, exchanges)
	}
}
