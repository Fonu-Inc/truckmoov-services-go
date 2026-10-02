package trackinghttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/internal/tracking/infrastructure"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

type tickingClock struct{ now *time.Time }

func (c tickingClock) Now() time.Time { return *c.now }

const devSecret = "test-dev-secret"

func blockedDevicesServer(gate *application.IngestGate) http.Handler {
	mux := http.NewServeMux()
	handler := NewHandler(nil, nil, nil, nil, "", nil, nil).WithGate(gate)
	handler.RegisterRoutes(httpx.NewRoutes(mux, true, nil, nil))
	roles := httpx.ServiceRoles{"auth": "FLEET_ADMIN", "fleet": "FLEET_ADMIN"}
	return httpx.WithServiceCallers(mux, identity.NewServiceTokenVerifier("tracking", devSecret), roles)
}

func callAs(service string, req *http.Request) {
	if service == "" {
		return
	}
	caller := identity.NewCaller(service, identity.NewServiceTokens(service, "/nonexistent", devSecret), "")
	caller.Authorize(req, "tracking", identity.ActingFor{TenantID: "system"})
}

func TestBlockedDevices_Report(t *testing.T) {
	now := time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)
	clock := tickingClock{now: &now}
	gate := application.NewIngestGate(nil, nil, clock, nil)
	gate.Replace(application.SuspensionSnapshot{
		Tenants: map[string]struct{}{"t-bad": {}},
		IMEIs:   map[string]struct{}{"imei-2": {}},
	}, now)
	known := &application.DeviceIdentity{IMEI: "imei-1", TenantID: "t-bad", DeviceID: "dev-1"}

	gate.Evaluate("t-bad", known, "imei-1")
	gate.Evaluate("t-bad", known, "imei-1")
	gate.Evaluate("t-ok", nil, "imei-2")
	gate.Evaluate("t-ok", nil, "imei-3")
	now = now.Add(24 * time.Hour)
	gate.Evaluate("t-bad", known, "imei-1")

	byIMEI := map[string]application.BlockedDevice{}
	for _, device := range gate.BlockedDevices().Devices {
		byIMEI[device.IMEI] = device
	}
	tests := []struct {
		imei           string
		today, total   int
		reason, tenant string
		deviceID       string
	}{
		{"imei-1", 1, 3, "tenant", "t-bad", "dev-1"},
		{"imei-2", 0, 1, "device", "t-ok", ""},
	}
	for _, tt := range tests {
		got, ok := byIMEI[tt.imei]
		if !ok {
			t.Fatalf("%s missing", tt.imei)
		}
		if got.DroppedToday != tt.today || got.DroppedTotal != tt.total || got.BlockedBy != tt.reason || got.TenantID != tt.tenant || got.DeviceID != tt.deviceID {
			t.Fatalf("%s: %+v", tt.imei, got)
		}
	}
	if _, ok := byIMEI["imei-3"]; ok {
		t.Fatal("a message that passed must not be listed")
	}
}

func TestBlockedDevices_RepeatedReadsKeepCounts(t *testing.T) {
	now := time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)
	gate := application.NewIngestGate(nil, nil, tickingClock{now: &now}, nil)
	gate.Replace(application.SuspensionSnapshot{Tenants: map[string]struct{}{"t": {}}}, now)
	gate.Evaluate("t", nil, "imei-1")
	for i := 0; i < 3; i++ {
		if got := gate.BlockedDevices().Devices[0].DroppedTotal; got != 1 {
			t.Fatalf("read %d changed count to %d", i, got)
		}
	}
}

func TestBlockedDevices_Endpoint(t *testing.T) {
	now := time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)
	gate := application.NewIngestGate(nil, nil, tickingClock{now: &now}, nil)
	gate.Replace(application.SuspensionSnapshot{Tenants: map[string]struct{}{"t": {}}}, now)
	gate.Evaluate("t", nil, "imei-1")
	tests := []struct {
		name    string
		service string
		method  string
		status  int
	}{
		{"auth reads", "auth", http.MethodGet, http.StatusOK},
		{"other service refused", "fleet", http.MethodGet, http.StatusNotFound},
		{"no caller refused", "", http.MethodGet, http.StatusNotFound},
		{"post refused", "auth", http.MethodPost, http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/tracking/internal/gate/blocked-devices", nil)
			callAs(tt.service, req)
			res := httptest.NewRecorder()
			blockedDevicesServer(gate).ServeHTTP(res, req)
			if res.Code != tt.status {
				t.Fatalf("status %d: %s", res.Code, res.Body.String())
			}
			if tt.status != http.StatusOK {
				return
			}
			var body application.BlockedDevicesReport
			decodeData(t, res.Body.Bytes(), &body)
			if body.CountsSince != "service_restart" || len(body.Devices) != 1 {
				t.Fatalf("body %s", res.Body.String())
			}
		})
	}
}

func quarantineServer() http.Handler {
	mux := http.NewServeMux()
	clock := tickingClock{now: &time.Time{}}
	quarantine := application.NewQuarantine(infrastructure.NewInMemoryQuarantineRepository(), clock)
	quarantine.Observe(context.Background(), "359779090077777")
	handler := NewHandler(nil, nil, nil, nil, "", nil, nil).WithQuarantine(quarantine)
	handler.RegisterRoutes(httpx.NewRoutes(mux, true, nil, nil))
	roles := httpx.ServiceRoles{"auth": "FLEET_ADMIN", "fleet": "FLEET_ADMIN"}
	return httpx.WithServiceCallers(mux, identity.NewServiceTokenVerifier("tracking", devSecret), roles)
}

func TestQuarantineList_OnlyAuthMayRead(t *testing.T) {
	server := quarantineServer()
	for service, want := range map[string]int{"auth": http.StatusOK, "fleet": http.StatusNotFound, "": http.StatusNotFound} {
		req := httptest.NewRequest(http.MethodGet, "/tracking/internal/quarantine", nil)
		callAs(service, req)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("service %q: want %d got %d", service, want, res.Code)
		}
		if service == "auth" && !strings.Contains(res.Body.String(), "359779090077777") {
			t.Fatalf("expected quarantined device in body, got %s", res.Body.String())
		}
	}
}
