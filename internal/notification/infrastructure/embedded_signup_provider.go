package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func (p *MetaWhatsAppEmbeddedSignupProvider) InspectToken(ctx context.Context, token string) (domain.WhatsAppTokenInfo, error) {
	query := url.Values{}
	query.Set("input_token", strings.TrimSpace(token))
	query.Set("access_token", p.appID+"|"+p.appSecret)
	endpoint := fmt.Sprintf("%s/debug_token?%s", p.baseURL, query.Encode())
	var response struct {
		Data struct {
			IsValid       bool  `json:"is_valid"`
			ExpiresAt     int64 `json:"expires_at"`
			GranularScope []struct {
				Scope     string   `json:"scope"`
				TargetIDs []string `json:"target_ids"`
			} `json:"granular_scopes"`
		} `json:"data"`
	}
	if err := p.getJSON(ctx, endpoint, "", &response); err != nil {
		return domain.WhatsAppTokenInfo{}, err
	}
	info := domain.WhatsAppTokenInfo{Valid: response.Data.IsValid, NeverExpires: response.Data.ExpiresAt == 0}
	for _, scope := range response.Data.GranularScope {
		if scope.Scope == "whatsapp_business_management" {
			info.BusinessAccountIDs = append(info.BusinessAccountIDs, scope.TargetIDs...)
		}
	}
	if response.Data.ExpiresAt > 0 {
		expires := time.Unix(response.Data.ExpiresAt, 0).UTC()
		info.ExpiresAt = &expires
	}
	return info, nil
}

// neverExpires answers false when Meta cannot say, so the usual exchange runs.
func (p *MetaWhatsAppEmbeddedSignupProvider) neverExpires(ctx context.Context, token string) bool {
	info, err := p.InspectToken(ctx, token)
	return err == nil && info.Valid && info.NeverExpires
}

const whatsAppPhoneFields = "display_phone_number,verified_name,name_status,quality_rating,status,messaging_limit_tier,platform_type"

type metaPhoneNumber struct {
	ID                 string `json:"id"`
	DisplayPhoneNumber string `json:"display_phone_number"`
	VerifiedName       string `json:"verified_name"`
	Name               string `json:"name"`
	NameStatus         string `json:"name_status"`
	QualityRating      string `json:"quality_rating"`
	Status             string `json:"status"`
	MessagingLimitTier string `json:"messaging_limit_tier"`
	PlatformType       string `json:"platform_type"`
}

func (n metaPhoneNumber) account(phoneNumberID string) domain.WhatsAppAccount {
	return domain.WhatsAppAccount{
		PhoneNumberID:      firstNonEmptyMetaString(n.ID, phoneNumberID),
		DisplayPhoneNumber: strings.TrimSpace(n.DisplayPhoneNumber),
		VerifiedName:       firstNonEmptyMetaString(n.VerifiedName, n.Name),
		NameStatus:         strings.TrimSpace(n.NameStatus),
		QualityRating:      strings.TrimSpace(n.QualityRating),
		NumberStatus:       strings.TrimSpace(n.Status),
		MessagingLimitTier: strings.TrimSpace(n.MessagingLimitTier),
		PlatformType:       strings.TrimSpace(n.PlatformType),
	}
}

func (p *MetaWhatsAppEmbeddedSignupProvider) ListPhoneNumbers(ctx context.Context, accessToken, businessAccountID string) ([]domain.WhatsAppAccount, error) {
	query := url.Values{}
	query.Set("fields", whatsAppPhoneFields)
	query.Set("limit", "100")
	endpoint := fmt.Sprintf("%s/%s/phone_numbers?%s", p.baseURL, strings.TrimSpace(businessAccountID), query.Encode())
	var response struct {
		Data []metaPhoneNumber `json:"data"`
	}
	if err := p.getJSON(ctx, endpoint, accessToken, &response); err != nil {
		return nil, err
	}
	numbers := make([]domain.WhatsAppAccount, 0, len(response.Data))
	for _, number := range response.Data {
		numbers = append(numbers, number.account(""))
	}
	return numbers, nil
}

func (p *MetaWhatsAppEmbeddedSignupProvider) GetPhoneNumberProfile(ctx context.Context, accessToken, phoneNumberID string) (domain.WhatsAppAccount, error) {
	query := url.Values{}
	query.Set("fields", whatsAppPhoneFields)
	endpoint := fmt.Sprintf("%s/%s?%s", p.baseURL, strings.TrimSpace(phoneNumberID), query.Encode())
	var response metaPhoneNumber
	if err := p.getJSON(ctx, endpoint, accessToken, &response); err != nil {
		return domain.WhatsAppAccount{}, err
	}
	return response.account(strings.TrimSpace(phoneNumberID)), nil
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
	return p.send(ctx, http.MethodPost, endpoint, accessToken, nil)
}

func (p *MetaWhatsAppEmbeddedSignupProvider) UnsubscribeBusinessApp(ctx context.Context, accessToken, businessAccountID string) error {
	endpoint := fmt.Sprintf("%s/%s/subscribed_apps", p.baseURL, strings.TrimSpace(businessAccountID))
	return p.send(ctx, http.MethodDelete, endpoint, accessToken, nil)
}

func (p *MetaWhatsAppEmbeddedSignupProvider) IsAppSubscribed(ctx context.Context, accessToken, businessAccountID string) (bool, error) {
	endpoint := fmt.Sprintf("%s/%s/subscribed_apps", p.baseURL, strings.TrimSpace(businessAccountID))
	var response struct {
		Data []struct {
			Data struct {
				ID string `json:"id"`
			} `json:"whatsapp_business_api_data"`
		} `json:"data"`
	}
	if err := p.getJSON(ctx, endpoint, accessToken, &response); err != nil {
		return false, err
	}
	for _, app := range response.Data {
		if app.Data.ID == p.appID {
			return true, nil
		}
	}
	return false, nil
}

func (p *MetaWhatsAppEmbeddedSignupProvider) RegisterPhoneNumber(ctx context.Context, accessToken, phoneNumberID, pin string) error {
	endpoint := fmt.Sprintf("%s/%s/register", p.baseURL, strings.TrimSpace(phoneNumberID))
	return p.send(ctx, http.MethodPost, endpoint, accessToken, map[string]string{"messaging_product": "whatsapp", "pin": pin})
}

func (p *MetaWhatsAppEmbeddedSignupProvider) DeregisterPhoneNumber(ctx context.Context, accessToken, phoneNumberID string) error {
	endpoint := fmt.Sprintf("%s/%s/deregister", p.baseURL, strings.TrimSpace(phoneNumberID))
	return p.send(ctx, http.MethodPost, endpoint, accessToken, map[string]string{"messaging_product": "whatsapp"})
}

// send makes a call whose response body is not needed.
func (p *MetaWhatsAppEmbeddedSignupProvider) send(ctx context.Context, method, endpoint, accessToken string, payload any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode whatsapp graph request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("build whatsapp graph request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return graphTransportError(err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read whatsapp graph response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return buildWhatsAppAPIError(resp.StatusCode, responseBody)
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
		return graphTransportError(err)
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

// graphTransportError drops the request URL, which can carry the app secret.
func graphTransportError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return fmt.Errorf("call whatsapp graph api: %w", err)
}

func firstNonEmptyMetaString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
