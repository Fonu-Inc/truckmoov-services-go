package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/identity"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

var (
	_ application.PushRecipientReader = (*AuthPushRecipientReader)(nil)
	_ application.VehicleScopeReader  = (*FleetVehicleScopeReader)(nil)
	_ application.PushCounter         = (*RedisPushCounter)(nil)
)

const pushRecipientCacheTTL = 60 * time.Second

type cachedRecipients struct {
	recipients []application.PushRecipient
	expiresAt  time.Time
}

// AuthPushRecipientReader asks auth for a tenant's users and keeps the answer
// for a minute, so a burst of events costs one call.
type AuthPushRecipientReader struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
	now        func() time.Time

	mu    sync.Mutex
	cache map[string]cachedRecipients
}

func NewAuthPushRecipientReader(baseURL string, caller *identity.Caller, httpClient *http.Client) *AuthPushRecipientReader {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &AuthPushRecipientReader{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		caller:     caller,
		httpClient: httpClient,
		now:        time.Now,
		cache:      map[string]cachedRecipients{},
	}
}

func (r *AuthPushRecipientReader) Recipients(ctx context.Context, tenantID string) ([]application.PushRecipient, error) {
	r.mu.Lock()
	cached, ok := r.cache[tenantID]
	r.mu.Unlock()
	if ok && r.now().Before(cached.expiresAt) {
		return cached.recipients, nil
	}

	var payload struct {
		Data []struct {
			ID               string          `json:"id"`
			Role             string          `json:"role"`
			OrganizationType string          `json:"organizationType"`
			Preferences      map[string]bool `json:"preferences"`
		} `json:"data"`
	}
	if err := getInternal(ctx, r.httpClient, r.caller, "auth", tenantID, r.baseURL+"/auth/internal/notification-recipients", &payload); err != nil {
		return nil, fmt.Errorf("read notification recipients: %w", err)
	}
	recipients := make([]application.PushRecipient, 0, len(payload.Data))
	for _, user := range payload.Data {
		recipients = append(recipients, application.PushRecipient{
			ID: user.ID, Role: user.Role, OrganizationType: user.OrganizationType, Preferences: user.Preferences,
		})
	}

	r.mu.Lock()
	// Expired entries are replaced, never swept: the map is bounded by tenants.
	r.cache[tenantID] = cachedRecipients{recipients: recipients, expiresAt: r.now().Add(pushRecipientCacheTTL)}
	r.mu.Unlock()
	return recipients, nil
}

// FleetVehicleScopeReader reads the same live-scope the realtime gateway uses.
type FleetVehicleScopeReader struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
}

func NewFleetVehicleScopeReader(baseURL string, caller *identity.Caller, httpClient *http.Client) *FleetVehicleScopeReader {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 3 * time.Second}
	}
	return &FleetVehicleScopeReader{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), caller: caller, httpClient: httpClient}
}

func (r *FleetVehicleScopeReader) VehicleScope(ctx context.Context, tenantID, userID string) ([]string, bool, error) {
	var payload struct {
		Data *struct {
			VehicleIDs *[]string `json:"vehicleIds"`
		} `json:"data"`
	}
	if err := getInternal(ctx, r.httpClient, r.caller, "fleet", tenantID, r.baseURL+"/fleet/internal/live-scope/"+url.PathEscape(userID), &payload); err != nil {
		return nil, false, fmt.Errorf("read live scope: %w", err)
	}
	if payload.Data == nil {
		return nil, false, fmt.Errorf("read live scope: no data")
	}
	if payload.Data.VehicleIDs == nil {
		return nil, true, nil
	}
	return *payload.Data.VehicleIDs, false, nil
}

func getInternal(ctx context.Context, client *http.Client, caller *identity.Caller, callee, tenantID, endpoint string, out any) error {
	if caller == nil || !strings.HasPrefix(endpoint, "http") {
		return fmt.Errorf("%s is not configured", callee)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if !caller.Authorize(req, callee, identity.ActingFor{TenantID: tenantID, LegacyRole: "DISPATCHER"}) {
		return fmt.Errorf("%s is not configured", callee)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", callee, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

const pushCounterPrefix = "notification:push-count:"

type RedisPushCounter struct {
	store *redisstore.Store
}

func NewRedisPushCounter(store *redisstore.Store) *RedisPushCounter {
	return &RedisPushCounter{store: store}
}

// Increment sets the expiry only when the key is created, so the window runs
// from the first push rather than sliding with every one.
func (c *RedisPushCounter) Increment(_ context.Context, key string, window time.Duration) (int64, error) {
	full := pushCounterPrefix + key
	replies, err := c.store.Pipeline(
		[]string{"SET", full, "0", "EX", strconv.Itoa(int(window.Seconds())), "NX"},
		[]string{"INCR", full},
	)
	if err != nil {
		return 0, err
	}
	count, ok := replies[1].(int64)
	if !ok {
		return 0, fmt.Errorf("unexpected INCR reply %T", replies[1])
	}
	return count, nil
}

// Decrement only touches a key that still exists, so it never recreates one
// without an expiry after its window has ended.
func (c *RedisPushCounter) Decrement(_ context.Context, key string) error {
	full := pushCounterPrefix + key
	_, err := c.store.Pipeline([]string{"EVAL", "if redis.call('EXISTS', KEYS[1]) == 1 then return redis.call('DECR', KEYS[1]) end return 0", "1", full})
	return err
}
