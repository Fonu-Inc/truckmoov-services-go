package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/truckmoov/services-go/pkg/identity"
)

func fleetVehiclesServer(t *testing.T, status int, body string) *FleetClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fleet/vehicles" || r.Header.Get(identity.ActingForTenantHeader) != "tenant-1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewFleetClient(server.URL, identity.NewCaller("dispatch", nil, ""), nil)
}

func TestOwnedVehicleIDsKeepsOnlyTheOperatorsVehicles(t *testing.T) {
	client := fleetVehiclesServer(t, http.StatusOK, `{"data":[
		{"id":"v1","ownerOperatorId":"oo-1"},
		{"id":"v2","ownerOperatorId":"oo-2"},
		{"id":"v3","ownerOperatorId":null},
		{"id":"v4","ownerOperatorId":"oo-1"}
	]}`)

	ids, err := client.OwnedVehicleIDs(context.Background(), "tenant-1", "oo-1")
	if err != nil || !slices.Equal(ids, []string{"v1", "v4"}) {
		t.Fatalf("expected [v1 v4], got %v err=%v", ids, err)
	}
}

func TestOwnedVehicleIDsMatchesNothingForABlankOperator(t *testing.T) {
	client := fleetVehiclesServer(t, http.StatusOK, `{"data":[{"id":"v3","ownerOperatorId":null}]}`)

	ids, err := client.OwnedVehicleIDs(context.Background(), "tenant-1", " ")
	if err != nil || len(ids) != 0 {
		t.Fatalf("a blank operator must own nothing, got %v err=%v", ids, err)
	}
}

func TestOwnedVehicleIDsReportsAFleetFailure(t *testing.T) {
	client := fleetVehiclesServer(t, http.StatusBadGateway, `{"status":false}`)

	if _, err := client.OwnedVehicleIDs(context.Background(), "tenant-1", "oo-1"); err == nil {
		t.Fatal("expected an error when fleet fails")
	}
}
