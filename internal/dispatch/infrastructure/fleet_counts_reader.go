package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/pkg/identity"
)

// FleetCountsReader asks fleet how many vehicles and drivers a tenant has —
// fleet's data, asked for rather than duplicated, the same pattern
// AuthEntitlementsReader uses to ask auth for plan quotas.
type FleetCountsReader struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
	now        func() time.Time
}

func NewFleetCountsReader(baseURL string, caller *identity.Caller, client *http.Client) *FleetCountsReader {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &FleetCountsReader{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		caller:     caller,
		httpClient: client,
		now:        time.Now,
	}
}

type fleetCountsResponse struct {
	Data *struct {
		VehicleCount int `json:"vehicleCount"`
		DriverCount  int `json:"driverCount"`
	} `json:"data"`
}

// TenantCounts returns (0, 0, nil) rather than an error when fleet is
// unreachable or the tenant has nothing — a capacity signal that fails soft,
// the same way MaxActiveJobs does, rather than breaking the whole network
// overview screen over one linked operator's fleet service being slow.
func (r *FleetCountsReader) TenantCounts(ctx context.Context, tenantID string) (int, int, error) {
	if r.baseURL == "" {
		return 0, 0, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/fleet/internal/tenant-counts", nil)
	if err != nil {
		return 0, 0, fmt.Errorf("build tenant counts request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	r.caller.Authorize(req, "fleet", identity.ActingFor{TenantID: tenantID, LegacyRole: "FLEET_ADMIN"})

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return 0, 0, nil //nolint:nilerr // Unreachable fleet must not block the screen; caller treats this as zero.
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, 0, nil
	}

	var envelope fleetCountsResponse
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return 0, 0, nil
	}
	return envelope.Data.VehicleCount, envelope.Data.DriverCount, nil
}
