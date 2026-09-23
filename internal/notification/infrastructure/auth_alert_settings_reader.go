package infrastructure

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"

	"github.com/truckmoov/services-go/pkg/identity"
)

const (
	alertSettingsTimeout = 3 * time.Second
	// Short: a change to who is alerted should take effect quickly, and the
	// call is cheap. Long enough that a burst of alerts makes one request.
	alertSettingsCacheTTL = 30 * time.Second
)

type cachedAlertSetting struct {
	setting   *application.AlertSetting
	fetchedAt time.Time
}

type alertSettingsPayload struct {
	Data *struct {
		AlertType   string   `json:"alertType"`
		Enabled     bool     `json:"enabled"`
		Channels    []string `json:"channels"`
		Recipients  []string `json:"recipients"`
		MinSeverity string   `json:"minSeverity"`
	} `json:"data"`
}

// AuthAlertSettingsReader reads alert configuration from the auth service.
//
// Auth owns who is told and on which channel. Holding a copy here would give
// the platform two answers, and the one that sends would be the stale one.
type AuthAlertSettingsReader struct {
	baseURL    string
	jwtSecret  string
	httpClient *http.Client
	now        func() time.Time

	mu    sync.Mutex
	cache map[string]cachedAlertSetting
}

func NewAuthAlertSettingsReader(baseURL, jwtSecret string, httpClient *http.Client) *AuthAlertSettingsReader {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: alertSettingsTimeout}
	}
	return &AuthAlertSettingsReader{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret:  strings.TrimSpace(jwtSecret),
		httpClient: httpClient,
		now:        time.Now,
		cache:      map[string]cachedAlertSetting{},
	}
}

func (r *AuthAlertSettingsReader) ForAlertType(
	ctx context.Context,
	alertType string,
) (*application.AlertSetting, error) {
	// Checked here, not only at the call site. The value reaches a URL path and
	// a cache key, and a future caller must not be able to bypass the check by
	// calling this directly. An unchecked "../.." would reach a different auth
	// endpoint carrying a platform-admin service token.
	if !application.IsKnownAlertType(alertType) {
		return nil, fmt.Errorf("unknown alert type")
	}

	if r.baseURL == "" || r.jwtSecret == "" {
		// Not configured to reach auth. Reported as an error rather than as
		// "nothing configured", so the alert is retried instead of dropped.
		return nil, fmt.Errorf("alert settings source is not configured")
	}

	if cached, ok := r.fromCache(alertType); ok {
		return cached, nil
	}

	req, err := http.NewRequestWithContext(
		// Escaped as well as allowlisted. The allowlist is what makes this safe;
		// the escaping is there so it stays safe if the allowlist ever widens.
		ctx, http.MethodGet, r.baseURL+"/auth/internal/alert-settings/"+url.PathEscape(alertType), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("build alert settings request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.buildServiceToken())

	response, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read alert settings: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read alert settings: auth returned %d", response.StatusCode)
	}

	var payload alertSettingsPayload
	if decodeErr := json.NewDecoder(response.Body).Decode(&payload); decodeErr != nil {
		return nil, fmt.Errorf("decode alert settings: %w", decodeErr)
	}

	var setting *application.AlertSetting
	if payload.Data != nil {
		setting = &application.AlertSetting{
			AlertType:   payload.Data.AlertType,
			Enabled:     payload.Data.Enabled,
			Channels:    payload.Data.Channels,
			Recipients:  payload.Data.Recipients,
			MinSeverity: payload.Data.MinSeverity,
		}
	}

	r.store(alertType, setting)
	return setting, nil
}

func (r *AuthAlertSettingsReader) fromCache(alertType string) (*application.AlertSetting, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cached, ok := r.cache[alertType]
	if !ok || r.now().Sub(cached.fetchedAt) >= alertSettingsCacheTTL {
		return nil, false
	}
	return cached.setting, true
}

// store caches one alert type's settings.
//
// The map is bounded by the allowlist rather than by an eviction policy: only a
// known type reaches here, and there are four of them. An unbounded map keyed
// by a value from an event payload would otherwise grow for as long as someone
// kept publishing new ones.
func (r *AuthAlertSettingsReader) store(alertType string, setting *application.AlertSetting) {
	if !application.IsKnownAlertType(alertType) {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[alertType] = cachedAlertSetting{setting: setting, fetchedAt: r.now()}
}

// buildServiceToken names the system tenant: platform alerts belong to no
// tenant, and the settings they read are platform-wide.
func (r *AuthAlertSettingsReader) buildServiceToken() string {
	payload := authServiceTokenPayload{
		Subject:  "notification-service",
		Email:    "notification-service@truckmoov.local",
		Role:     "PLATFORM_ADMIN",
		TenantID: identity.SystemTenantID,
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
