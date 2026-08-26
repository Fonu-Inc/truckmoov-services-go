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

// AuthEntitlementsReader reads a tenant's plan entitlements from auth.
//
// Auth owns the plan and what it includes; this asks rather than keeping a
// second copy, for the same reason FleetClient asks fleet for vehicle and
// driver details instead of reading fleet's tables directly.
type AuthEntitlementsReader struct {
	baseURL    string
	jwtSecret  string
	httpClient *http.Client
	now        func() time.Time
}

func NewAuthEntitlementsReader(baseURL, jwtSecret string, client *http.Client) *AuthEntitlementsReader {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &AuthEntitlementsReader{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret:  strings.TrimSpace(jwtSecret),
		httpClient: client,
		now:        time.Now,
	}
}

type entitlementsTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
}

// entitlementsResponse mirrors only the one field this reader needs. A quota
// is absent, the string "unlimited", or a whole number — the same "unset means
// unlimited, not zero" rule every other quota on the platform follows.
type entitlementsResponse struct {
	Data *struct {
		Quotas struct {
			ActiveJobs json.RawMessage `json:"active_jobs"`
		} `json:"quotas"`
	} `json:"data"`
}

// MaxActiveJobs returns nil for no cap — an unset limit, a tenant with no
// plan, or auth being unreachable all mean the same thing here: do not refuse
// an acceptance over a number this service could not obtain.
func (r *AuthEntitlementsReader) MaxActiveJobs(ctx context.Context, tenantID string) (*int, error) {
	if r.baseURL == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/auth/internal/usage/entitlements", nil)
	if err != nil {
		return nil, fmt.Errorf("build entitlements request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if token := r.buildServiceToken(tenantID); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, nil //nolint:nilerr // Unreachable auth must not block an accept; caller treats nil as no cap.
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}

	var envelope entitlementsResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil
	}
	if envelope.Data == nil || len(envelope.Data.Quotas.ActiveJobs) == 0 {
		return nil, nil
	}

	raw := strings.TrimSpace(string(envelope.Data.Quotas.ActiveJobs))
	if raw == "" || raw == "null" || raw == `"unlimited"` {
		return nil, nil
	}

	var max int
	if err := json.Unmarshal(envelope.Data.Quotas.ActiveJobs, &max); err != nil {
		return nil, nil
	}
	return &max, nil
}

func (r *AuthEntitlementsReader) buildServiceToken(tenantID string) string {
	if r.jwtSecret == "" {
		return ""
	}
	payload := entitlementsTokenPayload{
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
