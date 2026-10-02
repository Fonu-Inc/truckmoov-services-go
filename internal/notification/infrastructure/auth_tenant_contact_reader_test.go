package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

func TestAuthTenantContactReaderReadsTheContactForTheTenant(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(httpx.WithAuthContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := httpx.ClaimsFromRequest(r)
		if r.URL.Path != "/auth/internal/tenant-contact" || !ok || claims.TenantID != "tenant-b" {
			t.Fatalf("unexpected request %s for %+v", r.URL.Path, claims)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"message":"success","data":{"email":"ops@b.com","phone":"+2348012345678"}}`))
	}), identity.SharedSecretVerifier("secret")))
	defer server.Close()

	reader := NewAuthTenantContactReader(server.URL, identity.NewCaller("notification", nil, "secret"), server.Client())
	contact, found, err := reader.ContactFor(context.Background(), "tenant-b")

	if err != nil || !found || contact.Email != "ops@b.com" || contact.Phone != "+2348012345678" {
		t.Fatalf("unexpected contact %+v found=%v err=%v", contact, found, err)
	}
}

func TestAuthTenantContactReaderReportsNoContactAndFailures(t *testing.T) {
	t.Parallel()

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":true,"message":"success","data":null}`))
	}))
	defer empty.Close()
	reader := NewAuthTenantContactReader(empty.URL, identity.NewCaller("notification", nil, "secret"), empty.Client())
	if _, found, err := reader.ContactFor(context.Background(), "tenant-b"); found || err != nil {
		t.Fatalf("a tenant with nobody to tell is not an error, got found=%v err=%v", found, err)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer failing.Close()
	reader = NewAuthTenantContactReader(failing.URL, identity.NewCaller("notification", nil, "secret"), failing.Client())
	if _, _, err := reader.ContactFor(context.Background(), "tenant-b"); err == nil {
		t.Fatal("expected an error when auth refuses")
	}
	if _, _, err := NewAuthTenantContactReader("", nil, nil).ContactFor(context.Background(), "tenant-b"); err == nil {
		t.Fatal("expected an error when auth is not configured")
	}
}
