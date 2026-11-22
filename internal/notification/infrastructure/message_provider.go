package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const defaultWhatsAppAPIBaseURL = "https://graph.facebook.com/v23.0"

type whatsAppAccountRepository interface {
	FindAccountByTenantID(ctx context.Context, tenantID string) (domain.WhatsAppAccount, bool, error)
}

type whatsAppTokenStatusMarker interface {
	SetTokenStatus(ctx context.Context, tenantID, status string) error
}

type whatsAppTokenCipher interface {
	Decrypt(ciphertext, iv string) (string, error)
}

type MetaWhatsAppMessageProvider struct {
	baseURL             string
	accountRepository   whatsAppAccountRepository
	tokenCipher         whatsAppTokenCipher
	legacyPhoneNumberID string
	legacyAccessToken   string
	client              *http.Client
}

// SMSMessageProvider is one SMS sender. The notification service uses the auth
// client, which owns the provider credentials and call.
type SMSMessageProvider interface {
	Send(ctx context.Context, tenantID, channel, recipient, content string) (string, error)
	SendTracked(ctx context.Context, tenantID, channel, recipient, content string) (domain.MessageSendResult, error)
}

type MultiChannelMessageProvider struct {
	whatsApp *MetaWhatsAppMessageProvider
	sms      SMSMessageProvider
	email    *SMTPEmailMessageProvider
	push     *FCMPushProvider
}

func NewMetaWhatsAppMessageProvider(baseURL string, accountRepository whatsAppAccountRepository, tokenCipher whatsAppTokenCipher, legacyPhoneNumberID, legacyAccessToken string) *MetaWhatsAppMessageProvider {
	return NewMetaWhatsAppMessageProviderWithClient(baseURL, accountRepository, tokenCipher, legacyPhoneNumberID, legacyAccessToken, nil)
}

func NewMetaWhatsAppMessageProviderWithClient(baseURL string, accountRepository whatsAppAccountRepository, tokenCipher whatsAppTokenCipher, legacyPhoneNumberID, legacyAccessToken string, client *http.Client) *MetaWhatsAppMessageProvider {
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBaseURL == "" {
		trimmedBaseURL = defaultWhatsAppAPIBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	return &MetaWhatsAppMessageProvider{
		baseURL:             trimmedBaseURL,
		accountRepository:   accountRepository,
		tokenCipher:         tokenCipher,
		legacyPhoneNumberID: strings.TrimSpace(legacyPhoneNumberID),
		legacyAccessToken:   strings.TrimSpace(legacyAccessToken),
		client:              client,
	}
}

func NewMultiChannelMessageProvider(whatsApp *MetaWhatsAppMessageProvider, sms SMSMessageProvider, email *SMTPEmailMessageProvider) *MultiChannelMessageProvider {
	return &MultiChannelMessageProvider{whatsApp: whatsApp, sms: sms, email: email}
}

func (p *MultiChannelMessageProvider) WithPush(push *FCMPushProvider) *MultiChannelMessageProvider {
	p.push = push
	return p
}

func (p *MetaWhatsAppMessageProvider) Send(ctx context.Context, tenantID, channel, recipient, content string) (string, error) {
	result, err := p.SendTracked(ctx, tenantID, channel, recipient, content)
	return result.Status, err
}

// SendTracked is Send that also returns the wamid Meta issued, which is the
// only handle a later delivery, read or failed receipt carries.
func (p *MetaWhatsAppMessageProvider) SendTracked(ctx context.Context, tenantID, channel, recipient, content string) (domain.MessageSendResult, error) {
	if strings.EqualFold(strings.TrimSpace(channel), "whatsapp") == false && strings.TrimSpace(channel) != "" {
		return domain.MessageSendResult{Status: domain.StatusFailed}, fmt.Errorf("unsupported notification channel: %s", channel)
	}
	result, err := p.SendText(ctx, tenantID, recipient, content)
	return domain.MessageSendResult{Status: result.Status, ProviderMessageID: result.MessageID}, err
}

func (p *MultiChannelMessageProvider) Send(ctx context.Context, tenantID, channel, recipient, content string) (string, error) {
	result, err := p.SendTracked(ctx, tenantID, channel, recipient, content)
	return result.Status, err
}

// SendTracked routes by channel like Send, and passes the WhatsApp message id and
// the SMS request id through so delivery receipts can be matched. Email reports
// no id of its own.
func (p *MultiChannelMessageProvider) SendTracked(ctx context.Context, tenantID, channel, recipient, content string) (domain.MessageSendResult, error) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "", "whatsapp":
		if p.whatsApp == nil {
			return domain.MessageSendResult{Status: domain.StatusFailed}, errors.New("whatsapp provider is not configured")
		}
		return p.whatsApp.SendTracked(ctx, tenantID, "whatsapp", recipient, content)
	case "sms":
		if p.sms == nil {
			return domain.MessageSendResult{Status: domain.StatusFailed}, errors.New("sms provider is not configured")
		}
		return p.sms.SendTracked(ctx, tenantID, "sms", recipient, content)
	case "email":
		if p.email == nil {
			return domain.MessageSendResult{Status: domain.StatusFailed}, errors.New("email provider is not configured")
		}
		status, err := p.email.Send(ctx, tenantID, "email", recipient, content)
		return domain.MessageSendResult{Status: status}, err
	case domain.ChannelPush:
		if p.push == nil {
			return domain.MessageSendResult{Status: domain.StatusFailed}, errors.New("push provider is not configured")
		}
		status, err := p.push.Send(ctx, tenantID, domain.ChannelPush, recipient, content)
		return domain.MessageSendResult{Status: status}, err
	default:
		return domain.MessageSendResult{Status: domain.StatusFailed}, fmt.Errorf("unsupported notification channel: %s", channel)
	}
}

func (p *MetaWhatsAppMessageProvider) SendText(ctx context.Context, tenantID, recipient, content string) (domain.WhatsAppSendResult, error) {
	if strings.TrimSpace(recipient) == "" || strings.TrimSpace(content) == "" {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, errors.New("recipient and content are required")
	}
	phoneNumberID, accessToken, err := p.resolveCredentials(ctx, tenantID)
	if err != nil {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, err
	}

	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                normalizeWhatsAppRecipient(recipient),
		"type":              "text",
		"text": map[string]string{
			"body": strings.TrimSpace(content),
		},
	}

	return p.sendTracked(ctx, tenantID, phoneNumberID, accessToken, payload)
}

func (p *MetaWhatsAppMessageProvider) SendTemplate(ctx context.Context, request domain.WhatsAppTemplateMessageRequest) (domain.WhatsAppSendResult, error) {
	if strings.TrimSpace(request.Recipient) == "" || strings.TrimSpace(request.Name) == "" {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, errors.New("recipient and template name are required")
	}
	phoneNumberID, accessToken, err := p.resolveCredentials(ctx, request.TenantID)
	if err != nil {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, err
	}

	language := strings.TrimSpace(request.Language)
	if language == "" {
		language = "en_US"
	}

	template := map[string]any{
		"name": request.Name,
		"language": map[string]string{
			"code": language,
		},
	}
	if len(request.Components) > 0 {
		template["components"] = request.Components
	}

	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                normalizeWhatsAppRecipient(request.Recipient),
		"type":              "template",
		"template":          template,
	}

	return p.sendTracked(ctx, request.TenantID, phoneNumberID, accessToken, payload)
}

func (p *MetaWhatsAppMessageProvider) SendMedia(ctx context.Context, request domain.WhatsAppMediaMessageRequest) (domain.WhatsAppSendResult, error) {
	if strings.TrimSpace(request.Recipient) == "" {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, errors.New("recipient is required")
	}
	phoneNumberID, accessToken, err := p.resolveCredentials(ctx, request.TenantID)
	if err != nil {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, err
	}

	mediaType := strings.ToLower(strings.TrimSpace(request.MediaType))
	switch mediaType {
	case "image", "video", "audio", "document":
	default:
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, fmt.Errorf("unsupported whatsapp media type: %s", request.MediaType)
	}
	if strings.TrimSpace(request.MediaID) == "" && strings.TrimSpace(request.MediaLink) == "" {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, errors.New("media_id or media_link is required")
	}

	mediaPayload := map[string]any{}
	if strings.TrimSpace(request.MediaID) != "" {
		mediaPayload["id"] = strings.TrimSpace(request.MediaID)
	} else {
		mediaPayload["link"] = strings.TrimSpace(request.MediaLink)
	}
	if mediaType != "audio" && strings.TrimSpace(request.Caption) != "" {
		mediaPayload["caption"] = strings.TrimSpace(request.Caption)
	}
	if mediaType == "document" && strings.TrimSpace(request.Filename) != "" {
		mediaPayload["filename"] = strings.TrimSpace(request.Filename)
	}

	payload := map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                normalizeWhatsAppRecipient(request.Recipient),
		"type":              mediaType,
		mediaType:           mediaPayload,
	}

	return p.sendTracked(ctx, request.TenantID, phoneNumberID, accessToken, payload)
}

// DownloadMedia fetches an inbound media attachment by its Meta media id.
//
// Two calls, as the Graph API requires: the id resolves to a short-lived,
// signed URL (GET /{media-id}), and only that URL serves the bytes. Both
// calls carry the same bearer token — the second one is not a public CDN
// link despite looking like one.
func (p *MetaWhatsAppMessageProvider) DownloadMedia(ctx context.Context, tenantID, mediaID string) ([]byte, string, error) {
	trimmedMediaID := strings.TrimSpace(mediaID)
	if trimmedMediaID == "" {
		return nil, "", errors.New("media id is required")
	}
	_, accessToken, err := p.resolveCredentials(ctx, tenantID)
	if err != nil {
		return nil, "", err
	}

	var meta struct {
		URL         string `json:"url"`
		MimeType    string `json:"mime_type"`
		FileSize    int64  `json:"file_size"`
		ContentType string `json:"content_type"`
	}
	if err := p.getJSON(ctx, fmt.Sprintf("%s/%s", p.baseURL, trimmedMediaID), accessToken, &meta); err != nil {
		return nil, "", fmt.Errorf("resolve whatsapp media url: %w", err)
	}
	if strings.TrimSpace(meta.URL) == "" {
		return nil, "", errors.New("whatsapp media has no download url")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build whatsapp media request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download whatsapp media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, "", fmt.Errorf("whatsapp media download returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 25*1024*1024))
	if err != nil {
		return nil, "", fmt.Errorf("read whatsapp media: %w", err)
	}

	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = meta.MimeType
	}
	return data, contentType, nil
}

func (p *MetaWhatsAppMessageProvider) getJSON(ctx context.Context, url, accessToken string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build whatsapp request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("call whatsapp api: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read whatsapp response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return buildWhatsAppAPIError(resp.StatusCode, body)
	}
	return json.Unmarshal(body, dest)
}

func (p *MetaWhatsAppMessageProvider) resolveCredentials(ctx context.Context, tenantID string) (string, string, error) {
	trimmedTenantID := strings.TrimSpace(tenantID)
	if trimmedTenantID != "" && p.accountRepository != nil {
		account, ok, err := p.accountRepository.FindAccountByTenantID(ctx, trimmedTenantID)
		if err != nil {
			return "", "", fmt.Errorf("load whatsapp account: %w", err)
		}
		if ok {
			if p.tokenCipher == nil {
				return "", "", errors.New("whatsapp account cipher is not configured")
			}
			accessToken, err := p.tokenCipher.Decrypt(account.EncryptedAccessToken, account.AccessTokenIV)
			if err != nil {
				return "", "", fmt.Errorf("decrypt whatsapp access token: %w", err)
			}
			if strings.TrimSpace(account.PhoneNumberID) == "" || strings.TrimSpace(accessToken) == "" {
				return "", "", errors.New("whatsapp account is incomplete for this tenant")
			}
			return strings.TrimSpace(account.PhoneNumberID), strings.TrimSpace(accessToken), nil
		}
	}
	if p.legacyAccessToken != "" && p.legacyPhoneNumberID != "" {
		return p.legacyPhoneNumberID, p.legacyAccessToken, nil
	}
	return "", "", errors.New("whatsapp account is not connected for this tenant")
}

// sendTracked flags the tenant's account when Meta says its token is dead, so
// the setup page asks for a reconnect instead of still reading Connected.
func (p *MetaWhatsAppMessageProvider) sendTracked(ctx context.Context, tenantID, phoneNumberID, accessToken string, payload map[string]any) (domain.WhatsAppSendResult, error) {
	result, err := p.send(ctx, phoneNumberID, accessToken, payload)
	if apiErr, ok := domain.AsWhatsAppAPIError(err); ok && apiErr.IsTokenInvalid() {
		p.markTokenInvalid(ctx, tenantID)
	}
	return result, err
}

func (p *MetaWhatsAppMessageProvider) markTokenInvalid(ctx context.Context, tenantID string) {
	if marker, ok := p.accountRepository.(whatsAppTokenStatusMarker); ok && strings.TrimSpace(tenantID) != "" {
		_ = marker.SetTokenStatus(ctx, tenantID, domain.WhatsAppTokenNeedsReconnect)
	}
}

func (p *MetaWhatsAppMessageProvider) send(ctx context.Context, phoneNumberID, accessToken string, payload map[string]any) (domain.WhatsAppSendResult, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, fmt.Errorf("marshal whatsapp payload: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/%s/messages", p.baseURL, strings.TrimSpace(phoneNumberID)),
		bytes.NewReader(body),
	)
	if err != nil {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, fmt.Errorf("build whatsapp request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, fmt.Errorf("send whatsapp message: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, fmt.Errorf("read whatsapp response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return domain.WhatsAppSendResult{Status: domain.StatusFailed}, buildWhatsAppAPIError(resp.StatusCode, responseBody)
	}

	var response struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return domain.WhatsAppSendResult{Status: domain.StatusSent}, nil
	}

	result := domain.WhatsAppSendResult{Status: domain.StatusSent}
	if len(response.Messages) > 0 {
		result.MessageID = strings.TrimSpace(response.Messages[0].ID)
	}
	return result, nil
}

func normalizeWhatsAppRecipient(recipient string) string {
	normalized := strings.TrimSpace(recipient)
	normalized = strings.TrimPrefix(normalized, "whatsapp:")
	replacer := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	normalized = replacer.Replace(normalized)
	return strings.TrimPrefix(normalized, "+")
}

func buildWhatsAppAPIError(statusCode int, body []byte) error {
	var response struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    int    `json:"code"`
			Subcode int    `json:"error_subcode"`
		} `json:"error"`
	}
	apiErr := &domain.WhatsAppAPIError{HTTPStatus: statusCode, Body: string(body)}
	if err := json.Unmarshal(body, &response); err == nil {
		apiErr.Message = strings.TrimSpace(response.Error.Message)
		apiErr.Type = response.Error.Type
		apiErr.Code = response.Error.Code
		apiErr.Subcode = response.Error.Subcode
	}
	return apiErr
}
