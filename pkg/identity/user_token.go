package identity

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UserTokenIssuer is the iss claim auth puts on every signed user token.
const UserTokenIssuer = "truckmoov-auth"

// ErrInvalidToken is the only failure a verifier reports. Why a token was
// refused is not the caller's business.
var ErrInvalidToken = errors.New("invalid token")

// UserClaims is a verified user token.
type UserClaims struct {
	Subject  string
	Email    string
	Role     string
	TenantID string
	Portal   Portal
	Expires  time.Time
	// Legacy marks a token from before tokens were signed with a private key.
	// Its portal was inferred from the role rather than issued.
	Legacy bool
}

type userTokenPayload struct {
	Issuer   string `json:"iss"`
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Portal   Portal `json:"portal"`
	Expiry   int64  `json:"exp"`
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// UserTokenVerifier checks user tokens with auth's public keys. It can
// never mint one: only auth holds the private key.
type UserTokenVerifier struct {
	keys         map[string]ed25519.PublicKey
	legacySecret []byte
	now          func() time.Time
}

// NewUserTokenVerifier takes the public keys as "kid:base64url,..." so a key
// can be rotated by publishing the new one alongside the old. A non-empty
// legacySecret also accepts the older shared-secret tokens, which is the
// compatibility window while tokens issued before the switch expire.
func NewUserTokenVerifier(publicKeys, legacySecret string) (*UserTokenVerifier, error) {
	keys, err := parsePublicKeys(publicKeys)
	if err != nil {
		return nil, err
	}
	return &UserTokenVerifier{keys: keys, legacySecret: []byte(strings.TrimSpace(legacySecret)), now: time.Now}, nil
}

func parsePublicKeys(spec string) (map[string]ed25519.PublicKey, error) {
	keys := map[string]ed25519.PublicKey{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		kid, encoded, ok := strings.Cut(entry, ":")
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		if !ok || err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("user token public key %q is not kid:base64url of %d bytes", kid, ed25519.PublicKeySize)
		}
		keys[kid] = ed25519.PublicKey(raw)
	}
	return keys, nil
}

// SharedSecretVerifier accepts only the older shared-secret tokens: a local
// run with no keys configured.
func SharedSecretVerifier(secret string) *UserTokenVerifier {
	return &UserTokenVerifier{keys: map[string]ed25519.PublicKey{}, legacySecret: []byte(strings.TrimSpace(secret)), now: time.Now}
}

// Verify returns the claims of a valid, unexpired token.
func (v *UserTokenVerifier) Verify(token string) (UserClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	switch len(parts) {
	case 3:
		return v.verifySigned(parts)
	case 2:
		return v.verifyLegacy(parts)
	default:
		return UserClaims{}, ErrInvalidToken
	}
}

func (v *UserTokenVerifier) verifySigned(parts []string) (UserClaims, error) {
	var header tokenHeader
	if !decodeSegment(parts[0], &header) || header.Alg != "EdDSA" {
		return UserClaims{}, ErrInvalidToken
	}
	key, ok := v.keys[header.Kid]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if !ok || err != nil || !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), signature) {
		return UserClaims{}, ErrInvalidToken
	}
	var payload userTokenPayload
	if !decodeSegment(parts[1], &payload) || payload.Issuer != UserTokenIssuer || !payload.Portal.valid() {
		return UserClaims{}, ErrInvalidToken
	}
	return v.claims(payload, false)
}

func (v *UserTokenVerifier) verifyLegacy(parts []string) (UserClaims, error) {
	if len(v.legacySecret) == 0 {
		return UserClaims{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, v.legacySecret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) != 1 {
		return UserClaims{}, ErrInvalidToken
	}
	var payload userTokenPayload
	if !decodeSegment(parts[0], &payload) {
		return UserClaims{}, ErrInvalidToken
	}
	payload.Portal = PortalFor(payload.Role, payload.TenantID)
	return v.claims(payload, true)
}

func (v *UserTokenVerifier) claims(p userTokenPayload, legacy bool) (UserClaims, error) {
	if p.Subject == "" || p.Email == "" || p.Role == "" || p.TenantID == "" || p.Expiry <= v.now().Unix() {
		return UserClaims{}, ErrInvalidToken
	}
	return UserClaims{
		Subject:  p.Subject,
		Email:    p.Email,
		Role:     p.Role,
		TenantID: p.TenantID,
		Portal:   p.Portal,
		Expires:  time.Unix(p.Expiry, 0).UTC(),
		Legacy:   legacy,
	}, nil
}

func decodeSegment(segment string, into any) bool {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	return err == nil && json.Unmarshal(raw, into) == nil
}
