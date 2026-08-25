package infrastructure

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
)

const chargeRequestTimeout = 5 * time.Second

type authServiceTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
}

// AuthUsageCharger debits a tenant's wallet through the auth service, which
// owns the wallet and the rates.
//
// Authenticated with a short-lived token signed with the shared JWT secret, the
// same way this service already calls dispatch. The tenant travels in the
// signed claim rather than the body, so a caller cannot charge a tenant it does
// not hold a token for.
type AuthUsageCharger struct {
	baseURL    string
	jwtSecret  string
	httpClient *http.Client
	now        func() time.Time
}

func NewAuthUsageCharger(baseURL, jwtSecret string, httpClient *http.Client) *AuthUsageCharger {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: chargeRequestTimeout}
	}
	return &AuthUsageCharger{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret:  strings.TrimSpace(jwtSecret),
		httpClient: httpClient,
		now:        time.Now,
	}
}

func (c *AuthUsageCharger) Charge(ctx context.Context, charge application.UsageCharge) error {
	// Not configured means charging is off in this deployment. Sends continue
	// rather than every message failing, which is the safer default for a
	// misconfiguration that would otherwise stop all traffic.
	if c.baseURL == "" || c.jwtSecret == "" {
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

	token := c.buildServiceToken(charge.TenantID)
	if token == "" {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)

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

func (c *AuthUsageCharger) buildServiceToken(tenantID string) string {
	if strings.TrimSpace(tenantID) == "" {
		return ""
	}

	payload := authServiceTokenPayload{
		Subject:  "notification-service",
		Email:    "notification-service@truckmoov.local",
		Role:     "DISPATCHER",
		TenantID: strings.TrimSpace(tenantID),
		// Short-lived: this token only has to survive one request.
		Expiry: c.now().Add(5 * time.Minute).Unix(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return ""
	}

	encoded := base64.RawURLEncoding.EncodeToString(payloadBytes)
	mac := hmac.New(sha256.New, []byte(c.jwtSecret))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
