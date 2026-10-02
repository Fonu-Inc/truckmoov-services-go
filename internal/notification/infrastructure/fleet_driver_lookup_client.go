package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/identity"
)

var _ application.DriverLookup = (*FleetDriverLookupClient)(nil)

// FleetDriverLookupClient asks fleet which drivers, in any tenant, list a
// phone number. It acts for the platform, since the question crosses tenants.
type FleetDriverLookupClient struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
}

func NewFleetDriverLookupClient(baseURL string, caller *identity.Caller, client *http.Client) *FleetDriverLookupClient {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &FleetDriverLookupClient{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), caller: caller, httpClient: client}
}

func (c *FleetDriverLookupClient) DriversByPhone(ctx context.Context, phone string) ([]application.LinkedDriver, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("fleet service url is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/fleet/internal/drivers/by-phone?phone="+url.QueryEscape(phone), nil)
	if err != nil {
		return nil, fmt.Errorf("build driver lookup request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c.caller.Authorize(req, "fleet", identity.ActingFor{TenantID: identity.SystemTenantID, LegacyRole: "PLATFORM_ADMIN"})
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call fleet service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet service returned %d", resp.StatusCode)
	}
	var payload struct {
		Data struct {
			Items []struct {
				DriverID string `json:"driverId"`
				Name     string `json:"name"`
				TenantID string `json:"tenantId"`
				Status   string `json:"status"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode driver lookup: %w", err)
	}
	drivers := make([]application.LinkedDriver, 0, len(payload.Data.Items))
	for _, item := range payload.Data.Items {
		drivers = append(drivers, application.LinkedDriver{DriverID: item.DriverID, Name: item.Name, TenantID: item.TenantID, Status: item.Status})
	}
	return drivers, nil
}
