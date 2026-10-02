package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/identity"
)

var _ application.TenantContactReader = (*AuthTenantContactReader)(nil)

// AuthTenantContactReader asks auth who to tell at a tenant. Auth owns the
// users; this service keeps no copy.
type AuthTenantContactReader struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
}

func NewAuthTenantContactReader(baseURL string, caller *identity.Caller, httpClient *http.Client) *AuthTenantContactReader {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &AuthTenantContactReader{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), caller: caller, httpClient: httpClient}
}

func (r *AuthTenantContactReader) ContactFor(ctx context.Context, tenantID string) (application.TenantContact, bool, error) {
	if r.baseURL == "" || r.caller == nil {
		return application.TenantContact{}, false, fmt.Errorf("tenant contact source is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/auth/internal/tenant-contact", nil)
	if err != nil {
		return application.TenantContact{}, false, fmt.Errorf("build tenant contact request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if !r.caller.Authorize(req, "auth", identity.ActingFor{TenantID: tenantID, LegacyRole: "DISPATCHER"}) {
		return application.TenantContact{}, false, fmt.Errorf("tenant contact source is not configured")
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return application.TenantContact{}, false, fmt.Errorf("read tenant contact: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return application.TenantContact{}, false, fmt.Errorf("read tenant contact: auth returned %d", resp.StatusCode)
	}

	var payload struct {
		Data *struct {
			Email string `json:"email"`
			Phone string `json:"phone"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return application.TenantContact{}, false, fmt.Errorf("decode tenant contact: %w", err)
	}
	if payload.Data == nil {
		return application.TenantContact{}, false, nil
	}
	return application.TenantContact{Email: payload.Data.Email, Phone: payload.Data.Phone}, true, nil
}
