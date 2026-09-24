package identity

import (
	"slices"
	"testing"
)

func TestEveryRoleHasABundleInItsOwnPortal(t *testing.T) {
	for _, role := range []string{"SUPER_ADMIN", "SYSTEM_ADMIN", "PLATFORM_ADMIN", "GOVERNANCE_OFFICER", "INSTALLER", "ENTERPRISE_ADMIN", "FLEET_ADMIN",
		"AGGREGATOR_ADMIN", "OWNER_OPERATOR", "OPERATIONS_MANAGER", "DISPATCHER", "FINANCE_VIEWER"} {
		if _, ok := predefinedRoles[role]; !ok {
			t.Fatalf("no bundle for %s", role)
		}
	}
	if !slices.Contains(RolePermissions("DISPATCHER", "t1"), "tenant.dispatch.manage") {
		t.Fatal("expected a dispatcher to manage dispatch")
	}
	if RolePermissions("PLATFORM_ADMIN", "t1") != nil {
		t.Fatal("a system role inside a tenant must hold nothing")
	}
	if predefinedRoles["INSTALLER"].Portal != PortalInstaller {
		t.Fatal("expected the installer bundle in the installer portal")
	}
}
