package infrastructure

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FleetCountsReader asks fleet how many vehicles and drivers a tenant has —
// fleet's data, asked for rather than duplicated, the same pattern
// AuthEntitlementsReader uses to ask auth for plan quotas.
type FleetCountsReader struct {
	baseURL    string
	jwtSecret  string
	httpClient *http.Client
	now        func() time.Time
}

func NewFleetCountsReader(baseURL, jwtSecret string, client *http.Client) *FleetCountsReader {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &FleetCountsReader{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret:  strings.TrimSpace(jwtSecret),
		httpClient: client,
		now:        time.Now,
	}
}

type fleetCountsTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
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
	if token := r.buildServiceToken(tenantID); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

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

func (r *FleetCountsReader) buildServiceToken(tenantID string) string {
	if r.jwtSecret == "" {
		return ""
	}
	payload := fleetCountsTokenPayload{
		Subject:  "dispatch-service",
		Email:    "dispatch-service@truckmoov.local",
		Role:     "FLEET_ADMIN",
		TenantID: strings.TrimSpace(tenantID),
		Expiry:   r.now().Add(5 * time.Minute).Unix(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return ""
	}

	encoded := base64.RawURLEncoding.EncodeToString(payloadBytes)
	mac := hmac.New(sha256.New, []byte(r.jwtSecret))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
