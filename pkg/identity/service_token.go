package identity

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Headers a service call carries. The token says which service is calling;
// the acting-for headers say on whose behalf, and the receiver decides for
// itself what that caller may do there.
const (
	ServiceTokenHeader     = "X-Service-Token"
	ActingForTenantHeader  = "X-Acting-For-Tenant"
	ActingForUserHeader    = "X-Acting-For-User"
	serviceAccountPrefix   = "system:serviceaccount:"
	devServiceTokenIssuer  = "truckmoov-dev"
	devServiceTokenSeconds = 300
)

// ServiceCaller is a verified calling service.
type ServiceCaller struct {
	Service string
}

// ServiceTokens hands a caller the token to present to another service.
//
// In the cluster each service's pod has one Kubernetes service account token
// per service it calls, bound to that service as its audience and rotated by
// the kubelet; a token stolen from one call cannot be replayed against a
// different service. Outside the cluster, and only when a dev secret is set,
// it signs a short-lived token itself.
type ServiceTokens struct {
	self      string
	dir       string
	devSecret []byte
	now       func() time.Time
}

func NewServiceTokens(self, dir, devSecret string) *ServiceTokens {
	return &ServiceTokens{self: self, dir: dir, devSecret: devSecretOutsideCluster(devSecret), now: time.Now}
}

// For returns the token for calling callee, or "" when this process has no
// way to identify itself.
func (s *ServiceTokens) For(callee string) string {
	if s == nil {
		return ""
	}
	if raw, err := os.ReadFile(filepath.Join(s.dir, callee, "token")); err == nil {
		return strings.TrimSpace(string(raw))
	}
	if len(s.devSecret) == 0 {
		return ""
	}
	payload, _ := json.Marshal(map[string]any{
		"iss": devServiceTokenIssuer,
		"sub": serviceAccountPrefix + "dev:" + s.self,
		"aud": []string{callee},
		"exp": s.now().Add(devServiceTokenSeconds * time.Second).Unix(),
	})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + devSignature(s.devSecret, encoded)
}

// ServiceTokenVerifier checks the token a calling service presents.
type ServiceTokenVerifier struct {
	self      string
	keys      *clusterKeys
	devSecret []byte
	now       func() time.Time
}

// NewServiceTokenVerifier verifies against the cluster's service account
// signing keys when running in Kubernetes, and against the dev secret only
// outside it.
func NewServiceTokenVerifier(self, devSecret string) *ServiceTokenVerifier {
	v := &ServiceTokenVerifier{self: self, devSecret: devSecretOutsideCluster(devSecret), now: time.Now}
	if inCluster() {
		v.keys = newClusterKeys()
	}
	return v
}

type serviceTokenPayload struct {
	Issuer    string          `json:"iss"`
	Subject   string          `json:"sub"`
	Audience  json.RawMessage `json:"aud"`
	Expiry    int64           `json:"exp"`
	NotBefore int64           `json:"nbf"`
}

// Verify returns the calling service named by a valid token addressed to
// this service.
func (v *ServiceTokenVerifier) Verify(token string) (ServiceCaller, error) {
	if v == nil {
		return ServiceCaller{}, ErrInvalidToken
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	var payload serviceTokenPayload
	switch {
	case len(parts) == 3 && v.keys != nil:
		issuer, err := v.keys.verify(parts)
		if err != nil || !decodeSegment(parts[1], &payload) || payload.Issuer != issuer {
			return ServiceCaller{}, ErrInvalidToken
		}
	case len(parts) == 2 && len(v.devSecret) > 0:
		if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(devSignature(v.devSecret, parts[0]))) != 1 ||
			!decodeSegment(parts[0], &payload) || payload.Issuer != devServiceTokenIssuer {
			return ServiceCaller{}, ErrInvalidToken
		}
	default:
		return ServiceCaller{}, ErrInvalidToken
	}
	return v.caller(payload)
}

func (v *ServiceTokenVerifier) caller(p serviceTokenPayload) (ServiceCaller, error) {
	now := v.now().Unix()
	if p.Expiry <= now || p.NotBefore > now || !audienceIncludes(p.Audience, v.self) {
		return ServiceCaller{}, ErrInvalidToken
	}
	account, ok := strings.CutPrefix(p.Subject, serviceAccountPrefix)
	_, name, found := strings.Cut(account, ":")
	if !ok || !found || name == "" {
		return ServiceCaller{}, ErrInvalidToken
	}
	return ServiceCaller{Service: name}, nil
}

func audienceIncludes(raw json.RawMessage, self string) bool {
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, aud := range many {
			if aud == self {
				return true
			}
		}
		return false
	}
	var one string
	return json.Unmarshal(raw, &one) == nil && one == self
}

func devSignature(secret []byte, value string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func inCluster() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// The dev secret is refused inside the cluster, so a leaked secret cannot be
// used to impersonate a service there.
func devSecretOutsideCluster(secret string) []byte {
	if inCluster() || strings.TrimSpace(secret) == "" {
		return nil
	}
	return []byte(strings.TrimSpace(secret))
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (k jwk) rsaKey() (*rsa.PublicKey, error) {
	n, errN := base64.RawURLEncoding.DecodeString(k.N)
	e, errE := base64.RawURLEncoding.DecodeString(k.E)
	if k.Kty != "RSA" || errN != nil || errE != nil {
		return nil, fmt.Errorf("unsupported key %q", k.Kid)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
}

// clusterKeys fetches the cluster's service account signing keys, and fetches
// again when a token names a key it has not seen, at most once a minute.
type clusterKeys struct {
	fetch     func() (issuer string, keys []jwk, err error)
	mu        sync.Mutex
	issuer    string
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

func (c *clusterKeys) verify(parts []string) (string, error) {
	var header tokenHeader
	if !decodeSegment(parts[0], &header) || header.Alg != "RS256" {
		return "", ErrInvalidToken
	}
	key, issuer := c.lookup(header.Kid)
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if key == nil || err != nil {
		return "", ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return "", ErrInvalidToken
	}
	return issuer, nil
}

func (c *clusterKeys) lookup(kid string) (*rsa.PublicKey, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key, ok := c.keys[kid]; ok {
		return key, c.issuer
	}
	if time.Since(c.fetchedAt) < time.Minute {
		return nil, ""
	}
	c.fetchedAt = time.Now()
	issuer, fetched, err := c.fetch()
	if err != nil {
		return nil, ""
	}
	c.issuer = issuer
	c.keys = map[string]*rsa.PublicKey{}
	for _, k := range fetched {
		if key, keyErr := k.rsaKey(); keyErr == nil {
			c.keys[k.Kid] = key
		}
	}
	return c.keys[kid], c.issuer
}

const (
	clusterAPI        = "https://kubernetes.default.svc"
	serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"
)

func newClusterKeys() *clusterKeys {
	return &clusterKeys{fetch: fetchClusterKeys}
}

// fetchClusterKeys reads the issuer and signing keys the API server
// publishes for service account tokens, authenticating with this pod's own
// service account.
func fetchClusterKeys() (string, []jwk, error) {
	client, token, err := clusterClient()
	if err != nil {
		return "", nil, err
	}
	var discovery struct {
		Issuer string `json:"issuer"`
	}
	if err := getJSON(client, token, clusterAPI+"/.well-known/openid-configuration", &discovery); err != nil {
		return "", nil, err
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := getJSON(client, token, clusterAPI+"/openid/v1/jwks", &set); err != nil {
		return "", nil, err
	}
	return discovery.Issuer, set.Keys, nil
}
