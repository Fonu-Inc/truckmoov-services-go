package infrastructure

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const (
	defaultFCMBaseURL = "https://fcm.googleapis.com"
	fcmScope          = "https://www.googleapis.com/auth/firebase.messaging"
	// Renewed this long before Google's expiry, so a send never races it.
	fcmTokenSlack = 5 * time.Minute
)

type pushDeviceStore interface {
	TokensFor(ctx context.Context, tenantID, userID string) ([]string, error)
	DeleteToken(ctx context.Context, token string) error
}

type fcmServiceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// FCMPushProvider sends one push to every app install of a user through the
// FCM HTTP v1 API. The recipient is the user id; the content is a
// domain.PushMessage as JSON.
type FCMPushProvider struct {
	baseURL   string
	projectID string
	account   fcmServiceAccount
	key       *rsa.PrivateKey
	devices   pushDeviceStore
	client    *http.Client
	now       func() time.Time

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

// NewFCMPushProvider returns nil, nil when push is not configured, which the
// caller reports and carries on without.
func NewFCMPushProvider(baseURL, projectID, serviceAccountJSON string, devices pushDeviceStore, client *http.Client) (*FCMPushProvider, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(serviceAccountJSON) == "" {
		return nil, nil
	}
	var account fcmServiceAccount
	if err := json.Unmarshal([]byte(serviceAccountJSON), &account); err != nil {
		return nil, fmt.Errorf("parse FCM service account: %w", err)
	}
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil || account.ClientEmail == "" || account.TokenURI == "" {
		return nil, errors.New("FCM service account needs client_email, private_key and token_uri")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse FCM private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("FCM private key is not RSA")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultFCMBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &FCMPushProvider{
		baseURL:   strings.TrimRight(baseURL, "/"),
		projectID: strings.TrimSpace(projectID),
		account:   account,
		key:       key,
		devices:   devices,
		client:    client,
		now:       time.Now,
	}, nil
}

func (p *FCMPushProvider) Send(ctx context.Context, tenantID, _, userID, content string) (string, error) {
	var message domain.PushMessage
	if err := json.Unmarshal([]byte(content), &message); err != nil {
		return domain.StatusFailed, fmt.Errorf("decode push message: %w", err)
	}
	tokens, err := p.devices.TokensFor(ctx, tenantID, userID)
	if err != nil {
		return domain.StatusFailed, fmt.Errorf("read push devices: %w", err)
	}
	var failures []string
	for _, token := range tokens {
		if sendErr := p.sendToToken(ctx, token, message); sendErr != nil {
			failures = append(failures, sendErr.Error())
		}
	}
	if len(failures) > 0 {
		return domain.StatusFailed, fmt.Errorf("fcm: %s", strings.Join(failures, "; "))
	}
	return domain.StatusSent, nil
}

func (p *FCMPushProvider) sendToToken(ctx context.Context, token string, message domain.PushMessage) error {
	accessToken, err := p.token(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"message": fcmMessage(token, message)})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/v1/projects/%s/messages:send", p.baseURL, url.PathEscape(p.projectID)), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if deadToken(resp.StatusCode, responseBody) {
		return p.devices.DeleteToken(ctx, token)
	}
	return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
}

// deadToken reports an install that will never accept a push again. A bare
// INVALID_ARGUMENT can also mean a malformed message, so it only counts when
// FCM blames the token: deleting every device over our own bug would be worse
// than retrying.
func deadToken(status int, body []byte) bool {
	if status == http.StatusNotFound {
		return true
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	for _, detail := range payload.Error.Details {
		if detail.ErrorCode == "UNREGISTERED" {
			return true
		}
	}
	return payload.Error.Status == "INVALID_ARGUMENT" && strings.Contains(strings.ToLower(payload.Error.Message), "registration token")
}

func fcmMessage(token string, message domain.PushMessage) map[string]any {
	collapse := collapseID(message.CollapseKey)
	return map[string]any{
		"token":        token,
		"notification": map[string]string{"title": message.Title, "body": message.Body},
		"data":         map[string]string{"kind": message.Kind, "id": message.ID},
		"android": map[string]any{
			"collapse_key": collapse,
			"priority":     "HIGH",
		},
		"apns": map[string]any{
			"headers": map[string]string{"apns-collapse-id": collapse, "apns-priority": "10"},
			// thread-id is an aps payload key, not an HTTP header.
			"payload": map[string]any{"aps": map[string]any{"thread-id": message.Category}},
		},
	}
}

// collapseID keeps the key within APNs' 64-byte limit for apns-collapse-id.
func collapseID(key string) string {
	if len(key) <= 64 {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (p *FCMPushProvider) token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.accessToken != "" && p.now().Before(p.expiresAt.Add(-fcmTokenSlack)) {
		return p.accessToken, nil
	}
	assertion, err := p.signedAssertion()
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.account.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch FCM access token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&payload) != nil || payload.AccessToken == "" {
		return "", fmt.Errorf("fetch FCM access token: status %d", resp.StatusCode)
	}
	p.accessToken = payload.AccessToken
	p.expiresAt = p.now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	return p.accessToken, nil
}

func (p *FCMPushProvider) signedAssertion() (string, error) {
	issued := p.now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss":   p.account.ClientEmail,
		"scope": fcmScope,
		"aud":   p.account.TokenURI,
		"iat":   issued.Unix(),
		"exp":   issued.Add(time.Hour).Unix(),
	})
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign FCM assertion: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
