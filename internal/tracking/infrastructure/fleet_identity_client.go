package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/pkg/identity"
)

type identityRow struct {
	IMEI      string  `json:"imei"`
	TenantID  string  `json:"tenantId"`
	VehicleID *string `json:"vehicleId"`
	DeviceID  string  `json:"deviceId"`
}

func (r identityRow) toIdentity() application.DeviceIdentity {
	vehicle := ""
	if r.VehicleID != nil {
		vehicle = strings.TrimSpace(*r.VehicleID)
	}
	return application.DeviceIdentity{
		IMEI:        strings.TrimSpace(r.IMEI),
		TenantID:    strings.TrimSpace(r.TenantID),
		VehicleUUID: vehicle,
		DeviceID:    strings.TrimSpace(r.DeviceID),
	}
}

// FleetIdentityClient reads fleet's device identities: the full snapshot, page
// by page, and a single lookup for one IMEI.
type FleetIdentityClient struct {
	fleetURL string
	getter   serviceGetter
}

func NewFleetIdentityClient(fleetURL string, caller *identity.Caller) *FleetIdentityClient {
	return &FleetIdentityClient{
		fleetURL: strings.TrimRight(strings.TrimSpace(fleetURL), "/"),
		getter:   serviceGetter{caller: caller, client: &http.Client{Timeout: snapshotTimeout}},
	}
}

func (c *FleetIdentityClient) Page(ctx context.Context, cursor string, limit int) (application.IdentityPage, error) {
	var body struct {
		Identities []identityRow `json:"identities"`
		NextCursor *string       `json:"nextCursor"`
	}
	endpoint := fmt.Sprintf("%s/fleet/internal/device-identities?limit=%d&cursor=%s", c.fleetURL, limit, url.QueryEscape(cursor))
	if err := c.getter.get(ctx, endpoint, "fleet", &body); err != nil {
		return application.IdentityPage{}, err
	}
	page := application.IdentityPage{Identities: make([]application.DeviceIdentity, 0, len(body.Identities))}
	for _, row := range body.Identities {
		page.Identities = append(page.Identities, row.toIdentity())
	}
	if body.NextCursor != nil {
		page.NextCursor = *body.NextCursor
	}
	return page, nil
}

// Lookup returns (nil, nil) only when fleet answers 404.
func (c *FleetIdentityClient) Lookup(ctx context.Context, imei string) (*application.DeviceIdentity, error) {
	var row identityRow
	endpoint := fmt.Sprintf("%s/fleet/internal/device-identities/%s", c.fleetURL, url.PathEscape(imei))
	if err := c.getter.get(ctx, endpoint, "fleet", &row); err != nil {
		var status *statusError
		if errors.As(err, &status) && status.status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	found := row.toIdentity()
	return &found, nil
}
