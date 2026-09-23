package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/pkg/identity"
)

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func forbidden(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"you lack the permission"}`))
}

func withClaims(req *http.Request, claims *AuthClaims) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), authClaimsContextKey{}, claims))
}

var (
	tenantUser = &AuthClaims{Role: "FLEET_ADMIN", TenantID: "tenant-a", Portal: identity.PortalTenant}
	staff      = &AuthClaims{Role: "PLATFORM_ADMIN", TenantID: "system", Portal: identity.PortalAdmin}
	fleetCall  = &AuthClaims{Role: "FLEET_ADMIN", TenantID: "tenant-a", Portal: identity.PortalTenant, Service: "fleet"}
)

func serve(routes *Routes, mux *http.ServeMux, path string, claims *AuthClaims) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if claims != nil {
		req = withClaims(req, claims)
	}
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	return res
}

func buildRoutes(enforce bool) (*Routes, *http.ServeMux, *[]Refusal) {
	mux := http.NewServeMux()
	refusals := &[]Refusal{}
	routes := NewRoutes(mux, enforce, nil, func(r Refusal) { *refusals = append(*refusals, r) })
	routes.HandleFunc("/t", []Audience{Tenant}, SignedIn(), ok)
	routes.HandleFunc("/ts", []Audience{Tenant, Service}, SignedIn(), ok)
	routes.HandleFunc("/a", []Audience{Admin}, SignedIn(), ok)
	routes.HandleFunc("/p", []Audience{Public}, None(), ok)
	routes.HandleFunc("/deep", []Audience{Tenant}, SignedIn(), forbidden)
	return routes, mux, refusals
}

func TestEachRouteServesOnlyItsAudience(t *testing.T) {
	routes, mux, refusals := buildRoutes(true)
	cases := []struct {
		path   string
		claims *AuthClaims
		want   int
	}{
		{"/t", tenantUser, 200},
		{"/t", staff, 404},
		{"/t", fleetCall, 404},
		{"/t", nil, 404},
		{"/ts", fleetCall, 200},
		{"/a", staff, 200},
		{"/a", tenantUser, 404},
		{"/p", nil, 200},
	}
	for _, c := range cases {
		if got := serve(routes, mux, c.path, c.claims).Code; got != c.want {
			t.Fatalf("%s as %+v: got %d, want %d", c.path, c.claims, got, c.want)
		}
	}
	if len(*refusals) != 4 || (*refusals)[0].Path != "/t" || (*refusals)[0].Audience != "admin" {
		t.Fatalf("expected each refusal reported for IP blocking, got %+v", *refusals)
	}
}

// A refusal must be indistinguishable from a path that does not exist.
func TestRefusalLooksLikeAMissingRoute(t *testing.T) {
	routes, mux, _ := buildRoutes(true)
	missing := serve(routes, mux, "/nowhere", nil)

	for _, refused := range []*httptest.ResponseRecorder{
		serve(routes, mux, "/a", tenantUser),
		serve(routes, mux, "/deep", tenantUser),
	} {
		if refused.Code != missing.Code || refused.Body.String() != missing.Body.String() ||
			refused.Header().Get("Content-Type") != missing.Header().Get("Content-Type") {
			t.Fatalf("refusal %d %q %q differs from missing route %d %q %q",
				refused.Code, refused.Body.String(), refused.Header().Get("Content-Type"),
				missing.Code, missing.Body.String(), missing.Header().Get("Content-Type"))
		}
	}
}

func TestLogOnlyModeLetsRequestsThroughAndBlocksNobody(t *testing.T) {
	routes, mux, refusals := buildRoutes(false)

	if got := serve(routes, mux, "/a", tenantUser).Code; got != 200 {
		t.Fatalf("log-only mode must not refuse, got %d", got)
	}
	if got := serve(routes, mux, "/deep", tenantUser).Code; got != 403 {
		t.Fatalf("log-only mode must leave a handler's answer alone, got %d", got)
	}
	if len(*refusals) != 0 {
		t.Fatalf("log-only refusals must not reach IP blocking, got %+v", *refusals)
	}
}

func TestARouteWithNoAudienceCannotBeRegistered(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected registration without an audience to panic")
		}
	}()
	NewRoutes(http.NewServeMux(), true, nil, nil).HandleFunc("/x", nil, SignedIn(), ok)
}

// The portals refresh a token on 401; answering an expired one with 404
// would log users out instead.
func TestAnExpiredTokenIsToldToRefresh(t *testing.T) {
	routes, mux, _ := buildRoutes(true)
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("Authorization", "Bearer expired")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", res.Code)
	}
	_ = routes
}

func TestAUserLackingTheRoutePermissionIsRefusedOnceEnforced(t *testing.T) {
	mux := http.NewServeMux()
	routes := NewRoutes(mux, true, nil, nil).EnforcePermissions(true)
	routes.HandleFunc("/jobs", []Audience{Tenant}, On("tenant.dispatch"), ok)
	finance := &AuthClaims{Role: "FINANCE_VIEWER", TenantID: "t1", Portal: identity.PortalTenant}
	dispatcher := &AuthClaims{Role: "DISPATCHER", TenantID: "t1", Portal: identity.PortalTenant}
	post := func(claims *AuthClaims) int {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, withClaims(httptest.NewRequest(http.MethodPost, "/jobs", nil), claims))
		return res.Code
	}

	if got := post(dispatcher); got != 200 {
		t.Fatalf("a dispatcher manages dispatch, got %d", got)
	}
	if got := post(finance); got != 404 {
		t.Fatalf("a finance viewer only reads dispatch, got %d", got)
	}
	if got := serve(routes, mux, "/jobs", finance).Code; got != 200 {
		t.Fatalf("a finance viewer may read, got %d", got)
	}
}

func TestLivePermissionsWinOverTheRoleBundle(t *testing.T) {
	mux := http.NewServeMux()
	NewRoutes(mux, true, nil, nil).EnforcePermissions(true).HandleFunc("/jobs", []Audience{Tenant}, On("tenant.dispatch"), ok)
	demoted := &AuthClaims{Role: "DISPATCHER", TenantID: "t1", Portal: identity.PortalTenant, Permissions: []string{}}
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, withClaims(httptest.NewRequest(http.MethodGet, "/jobs", nil), demoted))
	if res.Code != 404 {
		t.Fatalf("permissions auth reports now must decide, got %d", res.Code)
	}
}

func TestARouteUsersReachMustNameAPermission(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a user route without a permission to panic")
		}
	}()
	NewRoutes(http.NewServeMux(), true, nil, nil).HandleFunc("/x", []Audience{Tenant}, None(), ok)
}
