package httpx

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/pkg/identity"
)

type authClaimsContextKey struct{}

type AuthClaims struct {
	Subject  string
	Email    string
	Role     string
	TenantID string
	Portal   identity.Portal
	Expires  time.Time
	// Service names the calling service when the request is a service call.
	Service string
	// Permissions is what the user may do right now, from auth; nil when
	// auth could not be asked.
	Permissions []string
	// OrganizationType is the kind of organisation the user belongs to, from
	// auth; empty when auth could not be asked or knows none.
	OrganizationType string
}

// WithAuthContext attaches the caller's claims when the request carries a
// valid token, and passes every request on either way.
func WithAuthContext(next http.Handler, verifier *identity.UserTokenVerifier) http.Handler {
	if verifier == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := resolveAuthClaims(r.Header.Get("Authorization"), verifier)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), authClaimsContextKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth rejects the request with 401 unless a valid Bearer token is
// present, unlike WithAuthContext which only optionally attaches claims and
// always calls next regardless of auth outcome.
func RequireAuth(next http.Handler, verifier *identity.UserTokenVerifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if verifier == nil {
			writeUnauthorized(w, "authentication is not configured")
			return
		}

		claims, ok := resolveAuthClaims(r.Header.Get("Authorization"), verifier)
		if !ok {
			writeUnauthorized(w, "missing or invalid authorization")
			return
		}

		ctx := context.WithValue(r.Context(), authClaimsContextKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeUnauthorized(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusUnauthorized, message)
}

func ClaimsFromRequest(r *http.Request) (*AuthClaims, bool) {
	if r == nil {
		return nil, false
	}
	claims, ok := r.Context().Value(authClaimsContextKey{}).(*AuthClaims)
	if !ok || claims == nil {
		return nil, false
	}
	return claims, true
}

func TenantIDFromRequest(r *http.Request) string {
	claims, ok := ClaimsFromRequest(r)
	if !ok || claims == nil {
		return ""
	}
	return claims.TenantID
}

func ParseToken(token string, verifier *identity.UserTokenVerifier) (*AuthClaims, bool) {
	if verifier == nil {
		return nil, false
	}
	return resolveAuthClaims("Bearer "+token, verifier)
}

func resolveAuthClaims(authorizationHeader string, verifier *identity.UserTokenVerifier) (*AuthClaims, bool) {
	fields := strings.Fields(strings.TrimSpace(authorizationHeader))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return nil, false
	}

	claims, err := verifier.Verify(fields[1])
	if err != nil {
		return nil, false
	}
	return &AuthClaims{
		Subject:  claims.Subject,
		Email:    claims.Email,
		Role:     claims.Role,
		TenantID: claims.TenantID,
		Portal:   claims.Portal,
		Expires:  claims.Expires,
	}, true
}
