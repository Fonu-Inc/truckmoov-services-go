package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

func TestAuthPushRecipientReaderCachesPerTenantForAMinute(t *testing.T) {
	calls := 0
	server := httptest.NewServer(httpx.WithAuthContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := httpx.ClaimsFromRequest(r)
		if r.URL.Path != "/auth/internal/notification-recipients" || !ok || claims.TenantID != "tenant-a" {
			t.Errorf("unexpected request %s for %+v", r.URL.Path, claims)
		}
		calls++
		_, _ = w.Write([]byte(`{"status":true,"message":"success","data":[{"id":"u1","role":"DISPATCHER","organizationType":"FLEET","preferences":{"pushAlerts":true,"geofenceAlerts":false}}]}`))
	}), identity.SharedSecretVerifier("secret")))
	defer server.Close()

	reader := NewAuthPushRecipientReader(server.URL, identity.NewCaller("notification", nil, "secret"), server.Client())
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	reader.now = func() time.Time { return now }

	users, err := reader.Recipients(context.Background(), "tenant-a")
	if err != nil || len(users) != 1 || users[0].ID != "u1" || users[0].Preferences["geofenceAlerts"] || !users[0].Preferences["pushAlerts"] {
		t.Fatalf("users=%+v err=%v", users, err)
	}
	now = now.Add(59 * time.Second)
	_, _ = reader.Recipients(context.Background(), "tenant-a")
	if calls != 1 {
		t.Fatalf("want a cached answer within the minute, got %d calls", calls)
	}
	now = now.Add(2 * time.Second)
	_, _ = reader.Recipients(context.Background(), "tenant-a")
	if calls != 2 {
		t.Fatalf("want a refetch after the minute, got %d calls", calls)
	}
}

func TestFleetVehicleScopeReader(t *testing.T) {
	replies := map[string]string{
		"/fleet/internal/live-scope/listed": `{"status":true,"data":{"vehicleIds":["v1","v2"]}}`,
		"/fleet/internal/live-scope/whole":  `{"status":true,"data":{"vehicleIds":null}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply, ok := replies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(reply))
	}))
	defer server.Close()
	reader := NewFleetVehicleScopeReader(server.URL, identity.NewCaller("notification", nil, "secret"), server.Client())
	ctx := context.Background()

	if ids, whole, err := reader.VehicleScope(ctx, "t", "listed"); err != nil || whole || len(ids) != 2 {
		t.Fatalf("listed: %v %v %v", ids, whole, err)
	}
	if _, whole, err := reader.VehicleScope(ctx, "t", "whole"); err != nil || !whole {
		t.Fatalf("null means the whole tenant: %v %v", whole, err)
	}
	if _, whole, err := reader.VehicleScope(ctx, "t", "unknown"); err == nil || whole {
		t.Fatal("a refused scope is an error, never the whole tenant")
	}
}
