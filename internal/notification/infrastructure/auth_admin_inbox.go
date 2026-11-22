package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/identity"
)

const adminInboxRequestTimeout = 5 * time.Second

var ErrAdminInboxNotConfigured = errors.New("admin inbox is not configured")

// AuthAdminInbox records in-app platform alerts in the auth service, which
// serves them to the admin portal's notification centre.
type AuthAdminInbox struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
}

func NewAuthAdminInbox(baseURL string, caller *identity.Caller, httpClient *http.Client) *AuthAdminInbox {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: adminInboxRequestTimeout}
	}
	return &AuthAdminInbox{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		caller:     caller,
		httpClient: httpClient,
	}
}

func (a *AuthAdminInbox) Record(ctx context.Context, alert application.AdminInboxAlert) error {
	if a.baseURL == "" || a.caller == nil {
		return ErrAdminInboxNotConfigured
	}

	body, err := json.Marshal(map[string]any{
		"eventId":   alert.EventID,
		"alertType": alert.AlertType,
		"severity":  alert.Severity,
		"title":     alert.Title,
		"body":      alert.Body,
	})
	if err != nil {
		return fmt.Errorf("build admin inbox request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, a.baseURL+"/auth/internal/admin-notifications", bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("build admin inbox request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if !a.caller.Authorize(req, "auth", identity.ActingFor{TenantID: identity.SystemTenantID, LegacyRole: "PLATFORM_ADMIN"}) {
		return ErrAdminInboxNotConfigured
	}

	response, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("record admin notification: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return fmt.Errorf("record admin notification: auth returned %d", response.StatusCode)
	}
	return nil
}
