package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/identity"
)

const chargeRequestTimeout = 5 * time.Second

// AuthUsageCharger debits a tenant's wallet through the auth service, which
// owns the wallet and the rates.
//
// Authenticated with a short-lived token signed with the shared JWT secret, the
// same way this service already calls dispatch. The tenant travels in the
// signed claim rather than the body, so a caller cannot charge a tenant it does
// not hold a token for.
type AuthUsageCharger struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
	now        func() time.Time
}

func NewAuthUsageCharger(baseURL string, caller *identity.Caller, httpClient *http.Client) *AuthUsageCharger {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: chargeRequestTimeout}
	}
	return &AuthUsageCharger{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		caller:     caller,
		httpClient: httpClient,
		now:        time.Now,
	}
}

func (c *AuthUsageCharger) Charge(ctx context.Context, charge application.UsageCharge) error {
	// Not configured means charging is off in this deployment. Sends continue
	// rather than every message failing, which is the safer default for a
	// misconfiguration that would otherwise stop all traffic.
	if c.baseURL == "" || c.caller == nil {
		return nil
	}

	body, err := json.Marshal(map[string]any{
		"channel":   charge.Channel,
		"quantity":  charge.Quantity,
		"reason":    charge.Reason,
		"reference": charge.Reference,
	})
	if err != nil {
		return fmt.Errorf("build charge request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.baseURL+"/auth/internal/usage/charge", bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("build charge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if !c.caller.Authorize(req, "auth", identity.ActingFor{TenantID: charge.TenantID, LegacyRole: "DISPATCHER"}) {
		return nil
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		// Auth unreachable. The send is refused rather than allowed through:
		// letting it pass would spend money nobody recorded, and the message
		// can be retried once auth is back.
		return fmt.Errorf("charge wallet: %w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusPaymentRequired:
		return application.ErrInsufficientFunds
	case http.StatusForbidden:
		return application.ErrChannelDisabled
	default:
		return fmt.Errorf("charge wallet: auth returned %d", response.StatusCode)
	}
}
