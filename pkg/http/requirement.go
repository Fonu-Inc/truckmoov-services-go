package httpx

import (
	"net/http"
	"slices"

	"github.com/truckmoov/services-go/pkg/identity"
)

// Requirement is what a user must hold to use a route. Services are admitted
// by the route's audience and the receiver's service grants instead.
type Requirement struct {
	resource string
	exact    []string
	signedIn bool
	open     bool
}

// On names the resource a route works on: GET and HEAD need its read
// permission, every other method its manage one.
func On(resource string) Requirement { return Requirement{resource: resource} }

// Needs lists the exact permissions a route needs.
func Needs(permissions ...string) Requirement { return Requirement{exact: permissions} }

// SignedIn is any signed-in user of the route's portal.
func SignedIn() Requirement { return Requirement{signedIn: true} }

// None is for routes that serve only the public or only services, which no
// user permission governs.
func None() Requirement { return Requirement{open: true} }

func (q Requirement) needed(method string) []string {
	switch {
	case q.signedIn, q.open:
		return nil
	case len(q.exact) > 0:
		return q.exact
	case method == http.MethodGet || method == http.MethodHead:
		return []string{q.resource + ".read"}
	default:
		return []string{q.resource + ".manage"}
	}
}

// declared is whether the route names what users need; None does not.
func (q Requirement) declared() bool {
	return q.signedIn || q.resource != "" || len(q.exact) > 0
}

// permitted reports whether a user holds what the route needs. When auth
// could not be asked, the user's role bundle stands in.
func permitted(claims *AuthClaims, q Requirement, method string) bool {
	held := claims.Permissions
	if held == nil {
		held = identity.RolePermissions(claims.Role, claims.TenantID)
	}
	for _, permission := range q.needed(method) {
		if !slices.Contains(held, permission) {
			return false
		}
	}
	return true
}
