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

// FleetClient looks up driver and vehicle details from the fleet service so the
// dispatch service can enrich job responses without owning that data itself.
type FleetClient struct {
	baseURL    string
	jwtSecret  string
	httpClient *http.Client
	now        func() time.Time
}

type FleetVehicle struct {
	PlateNumber string
	Model       string
}

type FleetDriver struct {
	Name string
}

type fleetInternalServiceTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
}

func NewFleetClient(baseURL, jwtSecret string, client *http.Client) *FleetClient {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &FleetClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret:  strings.TrimSpace(jwtSecret),
		httpClient: client,
		now:        time.Now,
	}
}

func (c *FleetClient) GetVehicle(ctx context.Context, tenantID, vehicleID string) (FleetVehicle, error) {
	var envelope struct {
		Data struct {
			PlateNumber string `json:"plateNumber"`
			Model       string `json:"model"`
		} `json:"data"`
	}
	if err := c.get(ctx, tenantID, "/fleet/vehicles/"+strings.TrimSpace(vehicleID), &envelope); err != nil {
		return FleetVehicle{}, err
	}
	return FleetVehicle{PlateNumber: envelope.Data.PlateNumber, Model: envelope.Data.Model}, nil
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

func (c *FleetClient) get(ctx context.Context, tenantID, path string, dest any) error {
	if c.baseURL == "" {
		return fmt.Errorf("fleet service url is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build fleet request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if token := c.buildInternalToken(tenantID); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call fleet service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fleet service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode fleet response: %w", err)
	}
	return nil
}

func (c *FleetClient) buildInternalToken(tenantID string) string {
	if strings.TrimSpace(c.jwtSecret) == "" || strings.TrimSpace(tenantID) == "" {
		return ""
	}
	payload := fleetInternalServiceTokenPayload{
		Subject:  "dispatch-service",
		Email:    "dispatch-service@truckmoov.local",
		Role:     "DISPATCHER",
		TenantID: strings.TrimSpace(tenantID),
		Expiry:   c.now().Add(5 * time.Minute).Unix(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	mac := hmac.New(sha256.New, []byte(c.jwtSecret))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature
}
