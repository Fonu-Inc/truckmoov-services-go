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

	"github.com/truckmoov/services-go/internal/dispatch/application"
)

// ComplianceStatusReader asks fleet for a tenant's compliance-document
// standing — fleet's data, asked for rather than duplicated, the same pattern
// FleetCountsReader uses for vehicle/driver counts.
type ComplianceStatusReader struct {
	baseURL    string
	jwtSecret  string
	httpClient *http.Client
	now        func() time.Time
}

func NewComplianceStatusReader(baseURL, jwtSecret string, client *http.Client) *ComplianceStatusReader {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &ComplianceStatusReader{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret:  strings.TrimSpace(jwtSecret),
		httpClient: client,
		now:        time.Now,
	}
}

type complianceStatusTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
}

type complianceStatusResponse struct {
	Data *struct {
		Total                 int `json:"total"`
		Valid                 int `json:"valid"`
		ExpiringSoon          int `json:"expiringSoon"`
		Expired               int `json:"expired"`
		ComplianceRatePercent int `json:"complianceRatePercent"`
	} `json:"data"`
}

// Snapshot returns a zero-value snapshot rather than an error when fleet is
// unreachable — an unavailable compliance read must not break the whole
// network overview screen over one linked operator.
func (r *ComplianceStatusReader) Snapshot(ctx context.Context, tenantID string) (application.ComplianceSnapshot, error) {
	if r.baseURL == "" {
		return application.ComplianceSnapshot{}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/fleet/compliance/documents/summary", nil)
	if err != nil {
		return application.ComplianceSnapshot{}, fmt.Errorf("build compliance snapshot request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if token := r.buildServiceToken(tenantID); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return application.ComplianceSnapshot{}, nil //nolint:nilerr // Unreachable fleet must not block the screen; caller treats this as zero.
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return application.ComplianceSnapshot{}, nil
	}

	var envelope complianceStatusResponse
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return application.ComplianceSnapshot{}, nil
	}

	return application.ComplianceSnapshot{
		Total:                 envelope.Data.Total,
		Valid:                 envelope.Data.Valid,
		ExpiringSoon:          envelope.Data.ExpiringSoon,
		Expired:               envelope.Data.Expired,
		ComplianceRatePercent: envelope.Data.ComplianceRatePercent,
	}, nil
}

func (r *ComplianceStatusReader) buildServiceToken(tenantID string) string {
	if r.jwtSecret == "" {
		return ""
	}
	payload := complianceStatusTokenPayload{
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
