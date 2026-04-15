package httpx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type authClaimsContextKey struct{}

type AuthClaims struct {
	Subject  string
	Email    string
	Role     string
	TenantID string
	Expires  time.Time
}

type accessTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
}

func WithAuthContext(next http.Handler, jwtSecret string) http.Handler {
	secret := strings.TrimSpace(jwtSecret)
	if secret == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := resolveAuthClaims(r.Header.Get("Authorization"), secret)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), authClaimsContextKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ClaimsFromRequest(r *http.Request) (*AuthClaims, bool) {
	if r == nil {
		return nil, false
	}
	claims, ok := r.Context().Value(authClaimsContextKey{}).(*AuthClaims)
	return claims, ok
}

func TenantIDFromRequest(r *http.Request) string {
	claims, ok := ClaimsFromRequest(r)
	if !ok || claims == nil {
		return ""
	}
	return claims.TenantID
}

func ParseToken(token, secret string) (*AuthClaims, bool) {
	return resolveAuthClaims("Bearer "+token, secret)
}

func resolveAuthClaims(authorizationHeader, secret string) (*AuthClaims, bool) {
	fields := strings.Fields(strings.TrimSpace(authorizationHeader))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return nil, false
	}

	token := strings.TrimSpace(fields[1])
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, false
	}

	encodedPayload := parts[0]
	signature := parts[1]
	expectedSignature := signTokenValue(encodedPayload, secret)
	if subtle.ConstantTimeCompare([]byte(signature), []byte(expectedSignature)) != 1 {
		return nil, false
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return nil, false
	}

	var payload accessTokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, false
	}

	if payload.Subject == "" || payload.Email == "" || payload.Role == "" || payload.TenantID == "" || payload.Expiry <= time.Now().Unix() {
		return nil, false
	}

	return &AuthClaims{
		Subject:  payload.Subject,
		Email:    payload.Email,
		Role:     payload.Role,
		TenantID: payload.TenantID,
		Expires:  time.Unix(payload.Expiry, 0).UTC(),
	}, true
}

func signTokenValue(encodedPayload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
