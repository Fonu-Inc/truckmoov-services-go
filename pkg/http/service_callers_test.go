package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/pkg/identity"
)

func serviceCall(t *testing.T, caller, callee, tenantID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/fleet/vehicles", nil)
	identity.NewCaller(caller, identity.NewServiceTokens(caller, t.TempDir(), "dev-secret"), "").
		Authorize(req, callee, identity.ActingFor{TenantID: tenantID})
	return req
}

func claimsSeen(t *testing.T, req *http.Request) *AuthClaims {
	t.Helper()
	var seen *AuthClaims
	handler := WithServiceCallers(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = ClaimsFromRequest(r)
	}), identity.NewServiceTokenVerifier("fleet", "dev-secret"), ServiceRoles{"dispatch": "DISPATCHER"})
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return seen
}

func TestServiceCallActsWithTheRoleTheReceiverGrants(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	claims := claimsSeen(t, serviceCall(t, "dispatch", "fleet", "tenant-a"))

	if claims == nil || claims.Service != "dispatch" || claims.Role != "DISPATCHER" || claims.TenantID != "tenant-a" {
		t.Fatalf("expected dispatch acting as DISPATCHER in tenant-a, got %+v", claims)
	}
	if claims.Portal != identity.PortalTenant {
		t.Fatalf("expected the tenant portal, got %q", claims.Portal)
	}
}

func TestServiceCallsTheReceiverDoesNotExpectAreIgnored(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	cases := map[string]*http.Request{
		"a caller with no grant here":     serviceCall(t, "tracking", "fleet", "tenant-a"),
		"a token for another service":     serviceCall(t, "dispatch", "notification", "tenant-a"),
		"a call acting for no tenant":     serviceCall(t, "dispatch", "fleet", ""),
		"a request with no service token": httptest.NewRequest(http.MethodGet, "/fleet/vehicles", nil),
	}
	for name, req := range cases {
		if claims := claimsSeen(t, req); claims != nil {
			t.Fatalf("%s: expected no claims, got %+v", name, claims)
		}
	}
}
