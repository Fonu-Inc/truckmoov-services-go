package identity

import (
	_ "embed"
	"encoding/json"
)

// predefined_roles.json is generated from the TypeScript definition, which is
// the one source of each role's permissions; the TypeScript tests fail when
// the two drift.
//
//go:embed predefined_roles.json
var predefinedRolesJSON []byte

type predefinedRole struct {
	Portal      Portal   `json:"portal"`
	Permissions []string `json:"permissions"`
}

var predefinedRoles = func() map[string]predefinedRole {
	roles := map[string]predefinedRole{}
	if err := json.Unmarshal(predefinedRolesJSON, &roles); err != nil {
		panic("predefined_roles.json: " + err.Error())
	}
	return roles
}()

// RolePermissions is a predefined role's bundle, held only in its own
// portal: a system role inside a tenant is worth nothing.
func RolePermissions(role, tenantID string) []string {
	bundle, ok := predefinedRoles[role]
	if !ok || bundle.Portal != PortalFor(role, tenantID) {
		return nil
	}
	return bundle.Permissions
}

// Holdable reports whether any predefined role carries the permission, which
// is how a route's permission is checked to be real.
func Holdable(permission string) bool {
	for _, role := range predefinedRoles {
		for _, held := range role.Permissions {
			if held == permission {
				return true
			}
		}
	}
	return false
}
