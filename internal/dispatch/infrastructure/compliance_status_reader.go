package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/pkg/identity"
)

// ComplianceStatusReader asks fleet for a tenant's compliance-document
// standing — fleet's data, asked for rather than duplicated, the same pattern
// FleetCountsReader uses for vehicle/driver counts.
type ComplianceStatusReader struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
	now        func() time.Time
}

func NewComplianceStatusReader(baseURL string, caller *identity.Caller, client *http.Client) *ComplianceStatusReader {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &ComplianceStatusReader{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		caller:     caller,
		httpClient: client,
		now:        time.Now,
	}
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
	r.caller.Authorize(req, "fleet", identity.ActingFor{TenantID: tenantID, LegacyRole: "FLEET_ADMIN"})

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
