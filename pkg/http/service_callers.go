package httpx

import (
	"context"
	"net/http"
	"strings"

	"github.com/truckmoov/services-go/pkg/identity"
)

// ServiceRoles is, for each service allowed to call this one, the role its
// calls act with inside the tenant they are made for. The receiver decides
// this; a caller no longer mints a token claiming whatever role it likes.
type ServiceRoles map[string]string

// WithServiceCallers attaches claims for a verified service call. It wraps
// the handler inside WithAuthContext, so a valid service token takes
// precedence over the older user-shaped token callers still send during the
// compatibility window.
func WithServiceCallers(next http.Handler, verifier *identity.ServiceTokenVerifier, roles ServiceRoles) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claims, ok := serviceClaims(r, verifier, roles); ok {
			r = r.WithContext(context.WithValue(r.Context(), authClaimsContextKey{}, claims))
		}
		next.ServeHTTP(w, r)
	})
}

func serviceClaims(r *http.Request, verifier *identity.ServiceTokenVerifier, roles ServiceRoles) (*AuthClaims, bool) {
	token := r.Header.Get(identity.ServiceTokenHeader)
	tenantID := strings.TrimSpace(r.Header.Get(identity.ActingForTenantHeader))
	if token == "" || tenantID == "" {
		return nil, false
	}
	caller, err := verifier.Verify(token)
	role, allowed := roles[caller.Service]
	if err != nil || !allowed {
		return nil, false
	}
	return &AuthClaims{
		Subject:  "service:" + caller.Service,
		Email:    caller.Service + "-service@truckmoov.local",
		Role:     role,
		TenantID: tenantID,
		Portal:   identity.PortalFor(role, tenantID),
		Service:  caller.Service,
	}, true
}
