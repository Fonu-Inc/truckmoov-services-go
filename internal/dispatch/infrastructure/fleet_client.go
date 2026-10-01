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

// fleetResponseLimit fits a tenant's whole vehicle list; a cut-off body fails to decode.
const fleetResponseLimit = 4 << 20

// FleetClient looks up driver and vehicle details from the fleet service so the
// dispatch service can enrich job responses without owning that data itself.
type FleetClient struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
	now        func() time.Time
}

type FleetVehicle struct {
	PlateNumber string
	Make        string
	Model       string
}

type FleetDriver struct {
	Name string
}

func NewFleetClient(baseURL string, caller *identity.Caller, client *http.Client) *FleetClient {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &FleetClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		caller:     caller,
		httpClient: client,
		now:        time.Now,
	}
}

func (c *FleetClient) GetVehicle(ctx context.Context, tenantID, vehicleID string) (FleetVehicle, error) {
	var envelope struct {
		Data struct {
			PlateNumber string `json:"plateNumber"`
			Make        string `json:"make"`
			Model       string `json:"model"`
		} `json:"data"`
	}
	if err := c.get(ctx, tenantID, "/fleet/vehicles/"+strings.TrimSpace(vehicleID), &envelope); err != nil {
		return FleetVehicle{}, err
	}
	return FleetVehicle{PlateNumber: envelope.Data.PlateNumber, Make: envelope.Data.Make, Model: envelope.Data.Model}, nil
}

func (c *FleetClient) GetDriver(ctx context.Context, tenantID, driverID string) (FleetDriver, error) {
	var envelope struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.get(ctx, tenantID, "/fleet/drivers/"+strings.TrimSpace(driverID), &envelope); err != nil {
		return FleetDriver{}, err
	}
	return FleetDriver{Name: envelope.Data.Name}, nil
}

// OwnedVehicleIDs lists the tenant's vehicles whose owner operator is the
// given user. Fleet scopes nothing for a service call, so the match is made here.
func (c *FleetClient) OwnedVehicleIDs(ctx context.Context, tenantID, ownerOperatorID string) ([]string, error) {
	ownerOperatorID = strings.TrimSpace(ownerOperatorID)
	if ownerOperatorID == "" {
		return nil, nil
	}
	var envelope struct {
		Data []struct {
			ID              string `json:"id"`
			OwnerOperatorID string `json:"ownerOperatorId"`
		} `json:"data"`
	}
	if err := c.get(ctx, tenantID, "/fleet/vehicles", &envelope); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, vehicle := range envelope.Data {
		if vehicle.OwnerOperatorID == ownerOperatorID {
			ids = append(ids, vehicle.ID)
		}
	}
	return ids, nil
}

func (c *FleetClient) get(ctx context.Context, tenantID, path string, dest any) error {
	if c.baseURL == "" {
		return fmt.Errorf("fleet service url is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build fleet request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c.caller.Authorize(req, "fleet", identity.ActingFor{TenantID: tenantID, LegacyRole: "DISPATCHER"})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call fleet service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, fleetResponseLimit))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fleet service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode fleet response: %w", err)
	}
	return nil
}
