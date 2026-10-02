package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AccessChangedSubject names a user whose access changed; every instance
// drops its cached copy.
const AccessChangedSubject = "tm.system.access.changed"

const (
	accessCacheTTL    = 30 * time.Second
	accessNoUserTTL   = 10 * time.Second
	userNotFoundText  = "user not found"
	notFoundBodyLimit = 4 * 1024
)

var errAuthRouteMissing = errors.New("auth answered 404 without a user-not-found body")

// Access is what a user may do right now, as auth sees it.
type Access struct {
	UserID           string   `json:"userId"`
	TenantID         string   `json:"tenantId"`
	Portal           Portal   `json:"portal"`
	Active           bool     `json:"active"`
	Permissions      []string `json:"permissions"`
	OrganizationType string   `json:"organizationType"`
}

// AccessLookup is the answer to one question to auth.
type AccessLookup int

const (
	// AccessUnknown means auth could not be asked, which is not the same as suspended.
	AccessUnknown AccessLookup = iota
	AccessFound
	// AccessNoUser means auth knows no such user.
	AccessNoUser
	// AccessUnavailable means auth's route did not answer as auth (wrong URL,
	// gateway): callers fail closed and nothing is cached.
	AccessUnavailable
)

type cachedAccess struct {
	access Access
	lookup AccessLookup
	at     time.Time
}

// AccessClient asks auth what a user may do, so a suspension or a role
// change applies on the user's next request instead of when their token
// expires.
type AccessClient struct {
	authURL string
	caller  *Caller
	client  *http.Client
	mu      sync.Mutex
	cache   map[string]cachedAccess
}

func NewAccessClient(authURL string, caller *Caller) *AccessClient {
	return &AccessClient{
		authURL: strings.TrimRight(strings.TrimSpace(authURL), "/"),
		caller:  caller,
		client:  &http.Client{Timeout: 2 * time.Second},
		cache:   map[string]cachedAccess{},
	}
}

// Forget drops a user's cached access.
func (c *AccessClient) Forget(userID string) {
	c.mu.Lock()
	delete(c.cache, userID)
	c.mu.Unlock()
}

// Get returns the user's access and whether auth knew the user.
func (c *AccessClient) Get(ctx context.Context, userID string) (Access, AccessLookup) {
	c.mu.Lock()
	cached, ok := c.cache[userID]
	c.mu.Unlock()
	if ok && time.Since(cached.at) < cacheTTL(cached.lookup) {
		return cached.access, cached.lookup
	}
	access, lookup, err := c.fetch(ctx, userID)
	if errors.Is(err, errAuthRouteMissing) {
		return Access{}, AccessUnavailable
	}
	if err != nil {
		return cached.access, cached.lookup
	}
	c.mu.Lock()
	c.cache[userID] = cachedAccess{access: access, lookup: lookup, at: time.Now()}
	c.mu.Unlock()
	return access, lookup
}

func cacheTTL(lookup AccessLookup) time.Duration {
	if lookup == AccessNoUser {
		return accessNoUserTTL
	}
	return accessCacheTTL
}

func isUserNotFound(body []byte) bool {
	var parsed struct {
		Message any `json:"message"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return false
	}
	message, ok := parsed.Message.(string)
	return ok && strings.EqualFold(strings.TrimSpace(message), userNotFoundText)
}

func (c *AccessClient) fetch(ctx context.Context, userID string) (Access, AccessLookup, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.authURL+"/auth/internal/access/"+url.PathEscape(userID), nil)
	if err != nil {
		return Access{}, AccessUnknown, err
	}
	if !c.caller.Authorize(req, "auth", ActingFor{}) {
		return Access{}, AccessUnknown, fmt.Errorf("cannot identify this service to auth")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return Access{}, AccessUnknown, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, notFoundBodyLimit))
		if isUserNotFound(body) {
			return Access{}, AccessNoUser, nil
		}
		return Access{}, AccessUnknown, errAuthRouteMissing
	}
	if resp.StatusCode != http.StatusOK {
		return Access{}, AccessUnknown, fmt.Errorf("auth answered %d", resp.StatusCode)
	}
	var envelope struct {
		Data Access `json:"data"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Access{}, AccessUnknown, err
	}
	return envelope.Data, AccessFound, nil
}
