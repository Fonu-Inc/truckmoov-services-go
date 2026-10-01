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

func fleetRecordServer(t *testing.T, status int, seen *string) *FleetClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.URL.EscapedPath()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":{"id":"x"}}`))
	}))
	t.Cleanup(server.Close)
	return NewFleetClient(server.URL, identity.NewCaller("dispatch", nil, ""), nil)
}

func TestDriverAndVehicleExistsReadFleetsAnswer(t *testing.T) {
	cases := []struct {
		status    int
		wantFound bool
		wantErr   bool
	}{
		{http.StatusOK, true, false},
		{http.StatusNotFound, false, false},
		{http.StatusInternalServerError, false, true},
		{http.StatusUnauthorized, false, true},
	}
	for _, tc := range cases {
		var seen string
		client := fleetRecordServer(t, tc.status, &seen)

		for name, exists := range map[string]func(context.Context, string, string) (bool, error){
			"driver":  client.DriverExists,
			"vehicle": client.VehicleExists,
		} {
			found, err := exists(context.Background(), "tenant-1", "id-1")
			if found != tc.wantFound || (err != nil) != tc.wantErr {
				t.Fatalf("%s on %d: expected found=%v err=%v, got found=%v err=%v", name, tc.status, tc.wantFound, tc.wantErr, found, err)
			}
		}
	}
}

func TestDriverExistsEscapesTheID(t *testing.T) {
	var seen string
	client := fleetRecordServer(t, http.StatusOK, &seen)

	_, _ = client.DriverExists(context.Background(), "tenant-1", "../vehicles/v1")
	if seen != "/fleet/drivers/..%2Fvehicles%2Fv1" {
		t.Fatalf("expected the id kept inside the drivers path, got %s", seen)
	}
}
