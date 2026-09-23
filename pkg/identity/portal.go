// Package identity is the one definition of who is calling: which portal a
// user signed in through, and which service a service call comes from. The
// TypeScript services share it through @shared/identity; the two are kept in
// step by the token fixtures both test against.
package identity

// SystemTenantID is the tenant internal platform staff belong to.
const SystemTenantID = "system"

// Portal is the front end a user signed in through. A token acts only
// inside its own portal.
type Portal string

const (
	PortalAdmin  Portal = "admin"
	PortalTenant Portal = "tenant"
)

var systemRoles = map[string]bool{
	"SUPER_ADMIN":    true,
	"SYSTEM_ADMIN":   true,
	"PLATFORM_ADMIN": true,
}

// IsSystemRole reports whether role governs the platform itself.
func IsSystemRole(role string) bool {
	return systemRoles[role]
}

// IsPlatformActor needs both halves: a system role held inside a customer
// tenant is not platform staff.
func IsPlatformActor(role, tenantID string) bool {
	return tenantID == SystemTenantID && IsSystemRole(role)
}

// PortalFor is the portal a user belongs to.
func PortalFor(role, tenantID string) Portal {
	if IsPlatformActor(role, tenantID) {
		return PortalAdmin
	}
	return PortalTenant
}

func (p Portal) valid() bool {
	return p == PortalAdmin || p == PortalTenant
}
