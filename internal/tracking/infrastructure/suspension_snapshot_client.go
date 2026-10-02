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

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/pkg/identity"
)

const (
	snapshotTimeout  = 5 * time.Second
	snapshotPageSize = 5000
	snapshotMaxPages = 1000
	snapshotMaxBody  = 16 * 1024 * 1024
)

type SuspensionSnapshotClient struct {
	authURL  string
	fleetURL string
	getter   serviceGetter
}

type serviceGetter struct {
	caller *identity.Caller
	client *http.Client
}

type statusError struct {
	callee string
	status int
}

func (e *statusError) Error() string { return fmt.Sprintf("%s answered %d", e.callee, e.status) }

func NewSuspensionSnapshotClient(authURL, fleetURL string, caller *identity.Caller) *SuspensionSnapshotClient {
	return &SuspensionSnapshotClient{
		authURL:  strings.TrimRight(strings.TrimSpace(authURL), "/"),
		fleetURL: strings.TrimRight(strings.TrimSpace(fleetURL), "/"),
		getter:   serviceGetter{caller: caller, client: &http.Client{Timeout: snapshotTimeout}},
	}
}

func (c *SuspensionSnapshotClient) Load(ctx context.Context) (application.SuspensionSnapshot, error) {
	tenants, err := c.loadTenants(ctx)
	if err != nil {
		return application.SuspensionSnapshot{}, fmt.Errorf("suspended tenants: %w", err)
	}
	imeis, err := c.loadDevices(ctx)
	if err != nil {
		return application.SuspensionSnapshot{}, fmt.Errorf("suspended devices: %w", err)
	}
	return application.SuspensionSnapshot{Tenants: tenants, IMEIs: imeis}, nil
}

func (c *SuspensionSnapshotClient) loadTenants(ctx context.Context) (map[string]struct{}, error) {
	var page struct {
		Tenants []struct {
			TenantID string `json:"tenantId"`
		} `json:"tenants"`
	}
	if err := c.getter.get(ctx, c.authURL+"/auth/internal/suspensions", "auth", &page); err != nil {
		return nil, err
	}
	tenants := make(map[string]struct{}, len(page.Tenants))
	for _, tenant := range page.Tenants {
		if id := strings.TrimSpace(tenant.TenantID); id != "" {
			tenants[id] = struct{}{}
		}
	}
	return tenants, nil
}

func (c *SuspensionSnapshotClient) loadDevices(ctx context.Context) (map[string]struct{}, error) {
	imeis := map[string]struct{}{}
	cursor := ""
	for pages := 0; pages < snapshotMaxPages; pages++ {
		var page struct {
			Devices []struct {
				IMEI string `json:"imei"`
			} `json:"devices"`
			NextCursor *string `json:"nextCursor"`
		}
		endpoint := fmt.Sprintf("%s/fleet/internal/suspended-devices?limit=%d&cursor=%s", c.fleetURL, snapshotPageSize, url.QueryEscape(cursor))
		if err := c.getter.get(ctx, endpoint, "fleet", &page); err != nil {
			return nil, err
		}
		for _, device := range page.Devices {
			if imei := strings.TrimSpace(device.IMEI); imei != "" {
				imeis[imei] = struct{}{}
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return imeis, nil
		}
		cursor = *page.NextCursor
	}
	return nil, fmt.Errorf("more than %d pages", snapshotMaxPages)
}

func (c serviceGetter) get(ctx context.Context, endpoint, callee string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if !c.caller.Authorize(req, callee, identity.ActingFor{}) {
		return fmt.Errorf("cannot identify this service to %s", callee)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &statusError{callee: callee, status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, snapshotMaxBody))
	if err != nil {
		return err
	}
	return decodeEnveloped(body, out)
}

// decodeEnveloped accepts both the {data: ...} envelope and a bare body.
func decodeEnveloped(body []byte, out any) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		return json.Unmarshal(envelope.Data, out)
	}
	return json.Unmarshal(body, out)
}
