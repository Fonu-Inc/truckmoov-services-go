package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type MetaWhatsAppEmbeddedSignupProvider struct {
	baseURL   string
	appID     string
	appSecret string
	client    *http.Client
}

func NewMetaWhatsAppEmbeddedSignupProvider(baseURL, appID, appSecret string) *MetaWhatsAppEmbeddedSignupProvider {
	return NewMetaWhatsAppEmbeddedSignupProviderWithClient(baseURL, appID, appSecret, nil)
}

func NewMetaWhatsAppEmbeddedSignupProviderWithClient(baseURL, appID, appSecret string, client *http.Client) *MetaWhatsAppEmbeddedSignupProvider {
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBaseURL == "" {
		trimmedBaseURL = defaultWhatsAppAPIBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &MetaWhatsAppEmbeddedSignupProvider{
		baseURL:   trimmedBaseURL,
		appID:     strings.TrimSpace(appID),
		appSecret: strings.TrimSpace(appSecret),
		client:    client,
	}
}

func (p *MetaWhatsAppEmbeddedSignupProvider) ExchangeCodeForLongLivedToken(ctx context.Context, code string) (string, error) {
	token, err := p.exchangeCodeForShortLivedToken(ctx, code)
	if err != nil {
		return "", err
	}
	// A signup configured for a system user token already returns one that never
	// expires, and Meta refuses to exchange it again.
	if p.neverExpires(ctx, token) {
		return token, nil
	}
	return p.exchangeForLongLivedToken(ctx, token)
}

// neverExpires answers false when Meta cannot say, so the usual exchange runs.
func (p *MetaWhatsAppEmbeddedSignupProvider) neverExpires(ctx context.Context, token string) bool {
	query := url.Values{}
	query.Set("input_token", strings.TrimSpace(token))
	query.Set("access_token", p.appID+"|"+p.appSecret)
	endpoint := fmt.Sprintf("%s/debug_token?%s", p.baseURL, query.Encode())
	var response struct {
		Data struct {
			IsValid   bool  `json:"is_valid"`
			ExpiresAt int64 `json:"expires_at"`
		} `json:"data"`
	}
	if err := p.getJSON(ctx, endpoint, "", &response); err != nil {
		return false
	}
	return response.Data.IsValid && response.Data.ExpiresAt == 0
}

func (p *MetaWhatsAppEmbeddedSignupProvider) GetPhoneNumberProfile(ctx context.Context, accessToken, phoneNumberID string) (domain.WhatsAppAccount, error) {
	query := url.Values{}
	query.Set("fields", "display_phone_number,verified_name,name")
	endpoint := fmt.Sprintf("%s/%s?%s", p.baseURL, strings.TrimSpace(phoneNumberID), query.Encode())
	var response struct {
		DisplayPhoneNumber string `json:"display_phone_number"`
		VerifiedName       string `json:"verified_name"`
		Name               string `json:"name"`
	}
	if err := p.getJSON(ctx, endpoint, accessToken, &response); err != nil {
		return domain.WhatsAppAccount{}, err
	}
	return domain.WhatsAppAccount{
		PhoneNumberID:      strings.TrimSpace(phoneNumberID),
		DisplayPhoneNumber: strings.TrimSpace(response.DisplayPhoneNumber),
		VerifiedName:       firstNonEmptyMetaString(response.VerifiedName, response.Name),
	}, nil
}

func (p *MetaWhatsAppEmbeddedSignupProvider) GetBusinessProfile(ctx context.Context, accessToken, businessAccountID string) (domain.WhatsAppAccount, error) {
	query := url.Values{}
	query.Set("fields", "name")
	endpoint := fmt.Sprintf("%s/%s?%s", p.baseURL, strings.TrimSpace(businessAccountID), query.Encode())
	var response struct {
		Name string `json:"name"`
	}
	if err := p.getJSON(ctx, endpoint, accessToken, &response); err != nil {
		return domain.WhatsAppAccount{}, err
	}
	return domain.WhatsAppAccount{
		BusinessAccountID: strings.TrimSpace(businessAccountID),
		BusinessName:      strings.TrimSpace(response.Name),
	}, nil
}

func (p *MetaWhatsAppEmbeddedSignupProvider) SubscribeBusinessApp(ctx context.Context, accessToken, businessAccountID string) error {
	endpoint := fmt.Sprintf("%s/%s/subscribed_apps", p.baseURL, strings.TrimSpace(businessAccountID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build whatsapp subscribed_apps request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("subscribe whatsapp app: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read whatsapp subscribed_apps response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return buildWhatsAppAPIError(resp.StatusCode, body)
	}
	return nil
}

func (p *MetaWhatsAppEmbeddedSignupProvider) exchangeCodeForShortLivedToken(ctx context.Context, code string) (string, error) {
	if strings.TrimSpace(p.appID) == "" || strings.TrimSpace(p.appSecret) == "" {
		return "", fmt.Errorf("whatsapp embedded signup is not configured; set WHATSAPP_APP_ID and WHATSAPP_APP_SECRET")
	}
	query := url.Values{}
	query.Set("client_id", p.appID)
	query.Set("client_secret", p.appSecret)
	query.Set("code", strings.TrimSpace(code))
	endpoint := fmt.Sprintf("%s/oauth/access_token?%s", p.baseURL, query.Encode())
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := p.getJSON(ctx, endpoint, "", &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return "", fmt.Errorf("facebook token exchange failed: missing access token")
	}
	return strings.TrimSpace(response.AccessToken), nil
}

func (p *MetaWhatsAppEmbeddedSignupProvider) exchangeForLongLivedToken(ctx context.Context, shortLivedToken string) (string, error) {
	query := url.Values{}
	query.Set("grant_type", "fb_exchange_token")
	query.Set("client_id", p.appID)
	query.Set("client_secret", p.appSecret)
	query.Set("fb_exchange_token", strings.TrimSpace(shortLivedToken))
	endpoint := fmt.Sprintf("%s/oauth/access_token?%s", p.baseURL, query.Encode())
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := p.getJSON(ctx, endpoint, "", &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return "", fmt.Errorf("long-lived token exchange failed: missing access token")
	}
	return strings.TrimSpace(response.AccessToken), nil
}

func (p *MetaWhatsAppEmbeddedSignupProvider) getJSON(ctx context.Context, endpoint, accessToken string, destination any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build whatsapp graph request: %w", err)
	}
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("call whatsapp graph api: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read whatsapp graph response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return buildWhatsAppAPIError(resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode whatsapp graph response: %w", err)
	}
	return nil
}

func firstNonEmptyMetaString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
