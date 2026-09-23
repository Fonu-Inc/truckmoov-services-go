package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ActingFor is whose behalf a service call is made on. The receiver works
// out what the calling service may do for them; the caller cannot claim it.
type ActingFor struct {
	TenantID string
	UserID   string
	// LegacyRole is the role the call used to impersonate. It is sent only
	// while receivers still accept the older tokens, and goes with them.
	LegacyRole string
}

// Caller identifies this service on its calls to others.
type Caller struct {
	self         string
	tokens       *ServiceTokens
	legacySecret []byte
	now          func() time.Time
}

// NewCaller signs calls with this service's own token. A non-empty
// legacySecret also sends the older user-shaped token, for receivers that do
// not read service tokens yet; it is empty once the compatibility window
// closes.
func NewCaller(self string, tokens *ServiceTokens, legacySecret string) *Caller {
	return &Caller{self: self, tokens: tokens, legacySecret: []byte(strings.TrimSpace(legacySecret)), now: time.Now}
}

// Authorize adds the headers identifying this service to a call to callee,
// and reports whether it could identify itself at all.
func (c *Caller) Authorize(req *http.Request, callee string, on ActingFor) bool {
	if c == nil {
		return false
	}
	identified := false
	if token := c.tokens.For(callee); token != "" {
		req.Header.Set(ServiceTokenHeader, token)
		identified = true
	}
	if on.TenantID = strings.TrimSpace(on.TenantID); on.TenantID != "" {
		req.Header.Set(ActingForTenantHeader, on.TenantID)
	}
	if on.UserID = strings.TrimSpace(on.UserID); on.UserID != "" {
		req.Header.Set(ActingForUserHeader, on.UserID)
	}
	if legacy := c.legacyToken(on); legacy != "" {
		req.Header.Set("Authorization", "Bearer "+legacy)
		identified = true
	}
	return identified
}

func (c *Caller) legacyToken(on ActingFor) string {
	if len(c.legacySecret) == 0 || on.LegacyRole == "" || on.TenantID == "" {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{
		"sub":      c.self + "-service",
		"email":    c.self + "-service@truckmoov.local",
		"role":     on.LegacyRole,
		"tenantId": on.TenantID,
		"exp":      c.now().Add(5 * time.Minute).Unix(),
	})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, c.legacySecret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
