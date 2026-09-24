package identity

import "testing"

func TestEachAccountBelongsToOnePortal(t *testing.T) {
	cases := []struct {
		role, tenant string
		want         Portal
	}{
		{"PLATFORM_ADMIN", SystemTenantID, PortalAdmin},
		{"FLEET_ADMIN", "t1", PortalTenant},
		{RoleInstaller, SystemTenantID, PortalInstaller},
		{RoleInstaller, "t1", PortalTenant},
		{"PLATFORM_ADMIN", "t1", PortalTenant},
	}
	for _, c := range cases {
		if got := PortalFor(c.role, c.tenant); got != c.want {
			t.Fatalf("%s in %s: got %s, want %s", c.role, c.tenant, got, c.want)
		}
	}
}

func TestAnInstallerIsNeverPlatformStaff(t *testing.T) {
	if IsPlatformActor(RoleInstaller, SystemTenantID) || IsSystemRole(RoleInstaller) {
		t.Fatal("an installer must never pass as platform staff")
	}
	if !PortalInstaller.valid() || Portal("partner").valid() {
		t.Fatal("expected installer to be a known portal and nothing else")
	}
}
