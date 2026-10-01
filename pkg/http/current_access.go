package httpx

import (
	"context"
	"net/http"

	"github.com/truckmoov/services-go/pkg/identity"
)

// WithCurrentAccess checks a signed-in user against their account as it is
// now. A suspended or deleted account loses its claims, so the request is
// treated as carrying no valid token. When auth cannot be asked, the
// verified token stands, rather than refusing everyone while auth is down.
func WithCurrentAccess(next http.Handler, client *identity.AccessClient) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFromRequest(r)
		if !ok || claims == nil || claims.Service != "" || client == nil {
			next.ServeHTTP(w, r)
			return
		}
		access, lookup := client.Get(r.Context(), claims.Subject)
		switch {
		case lookup == identity.AccessNoUser, lookup == identity.AccessFound && !access.Active:
			r = r.WithContext(context.WithValue(r.Context(), authClaimsContextKey{}, (*AuthClaims)(nil)))
		case lookup == identity.AccessFound:
			current := *claims
			current.Permissions = access.Permissions
			current.OrganizationType = access.OrganizationType
			r = r.WithContext(context.WithValue(r.Context(), authClaimsContextKey{}, &current))
		}
		next.ServeHTTP(w, r)
	})
}
