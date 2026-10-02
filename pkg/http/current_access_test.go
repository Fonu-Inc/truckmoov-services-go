package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/pkg/identity"
)

func accessServer(t *testing.T, status int, access identity.Access) *identity.AccessClient {
	t.Helper()
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if status == 404 {
			_ = json.NewEncoder(w).Encode(map[string]any{"statusCode": 404, "message": "user not found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": access})
	}))
	t.Cleanup(server.Close)
	caller := identity.NewCaller("dispatch", identity.NewServiceTokens("dispatch", t.TempDir(), "dev"), "")
	return identity.NewAccessClient(server.URL, caller)
}

func claimsAfter(t *testing.T, client *identity.AccessClient) (*AuthClaims, bool) {
	t.Helper()
	var seen *AuthClaims
	var ok bool
	req := withClaims(httptest.NewRequest(http.MethodGet, "/dispatch/jobs", nil), &AuthClaims{Subject: "u1", Role: "DISPATCHER", TenantID: "t1"})
	WithCurrentAccess(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, ok = ClaimsFromRequest(r)
	}), client).ServeHTTP(httptest.NewRecorder(), req)
	return seen, ok
}

func TestASuspendedAccountLosesItsClaims(t *testing.T) {
	if _, ok := claimsAfter(t, accessServer(t, 200, identity.Access{UserID: "u1", Active: false})); ok {
		t.Fatal("a suspended account must not keep acting on a still-valid token")
	}
}

func TestADeletedAccountLosesItsClaims(t *testing.T) {
	if _, ok := claimsAfter(t, accessServer(t, 404, identity.Access{})); ok {
		t.Fatal("a deleted account must not keep acting")
	}
}

func TestAnActiveAccountCarriesItsCurrentPermissions(t *testing.T) {
	claims, ok := claimsAfter(t, accessServer(t, 200, identity.Access{UserID: "u1", Active: true, Permissions: []string{"tenant.dispatch.read"}}))
	if !ok || len(claims.Permissions) != 1 || claims.Permissions[0] != "tenant.dispatch.read" {
		t.Fatalf("expected current permissions, got %+v", claims)
	}
}

func TestTheTokenStandsWhenAuthCannotBeAsked(t *testing.T) {
	if _, ok := claimsAfter(t, accessServer(t, 503, identity.Access{})); !ok {
		t.Fatal("an unreachable auth must not sign everyone out")
	}
}

func TestAnActiveAccountCarriesItsOrganizationType(t *testing.T) {
	claims, ok := claimsAfter(t, accessServer(t, 200, identity.Access{UserID: "u1", Active: true, OrganizationType: "enterprise"}))
	if !ok || claims.OrganizationType != "enterprise" {
		t.Fatalf("expected the organisation type from auth, got %+v", claims)
	}
}

func TestAMisroutedAuthFailsClosedWith503(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	caller := identity.NewCaller("dispatch", identity.NewServiceTokens("dispatch", t.TempDir(), "dev"), "")
	client := identity.NewAccessClient(server.URL, caller)
	req := withClaims(httptest.NewRequest(http.MethodGet, "/dispatch/jobs", nil), &AuthClaims{Subject: "u1", TenantID: "t1"})
	rec := httptest.NewRecorder()
	called := false
	WithCurrentAccess(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }), client).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("expected 503 without reaching handler, got %d called=%v", rec.Code, called)
	}
}
