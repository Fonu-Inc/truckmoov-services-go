package infrastructure

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const defaultWhatsAppAPIBaseURL = "https://graph.facebook.com/v18.0"

type whatsAppAccountRepository interface {
	FindAccountByTenantID(ctx context.Context, tenantID string) (domain.WhatsAppAccount, bool, error)
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

// DotgoSMSMessageProvider sends SMS through Dotgo's REST API, the same one
// fonu-api uses: one POST per message to the account's Messages resource,
// authenticated with the account token. The account is the transactional one,
// which is what job and installer notices are.
type DotgoSMSMessageProvider struct {
	baseURL     string
	accountID   string
	apiToken    string
	senderMask  string
	callbackURL string
	client      *http.Client
}

type MultiChannelMessageProvider struct {
	whatsApp *MetaWhatsAppMessageProvider
	sms      *DotgoSMSMessageProvider
	email    *SMTPEmailMessageProvider
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

func NewDotgoSMSMessageProvider(baseURL, accountID, apiToken, senderMask, callbackURL string) *DotgoSMSMessageProvider {
	return NewDotgoSMSMessageProviderWithClient(baseURL, accountID, apiToken, senderMask, callbackURL, nil)
}

func NewDotgoSMSMessageProviderWithClient(baseURL, accountID, apiToken, senderMask, callbackURL string, client *http.Client) *DotgoSMSMessageProvider {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &DotgoSMSMessageProvider{
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		accountID:   strings.TrimSpace(accountID),
		apiToken:    strings.TrimSpace(apiToken),
		senderMask:  strings.TrimSpace(senderMask),
		callbackURL: strings.TrimSpace(callbackURL),
		client:      client,
	}
}

func NewMultiChannelMessageProvider(whatsApp *MetaWhatsAppMessageProvider, sms *DotgoSMSMessageProvider, email *SMTPEmailMessageProvider) *MultiChannelMessageProvider {
	return &MultiChannelMessageProvider{whatsApp: whatsApp, sms: sms, email: email}
}

func (p *MetaWhatsAppMessageProvider) Send(ctx context.Context, tenantID, channel, recipient, content string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(channel), "whatsapp") == false && strings.TrimSpace(channel) != "" {
		return domain.StatusFailed, fmt.Errorf("unsupported notification channel: %s", channel)
	}
	result, err := p.SendText(ctx, tenantID, recipient, content)
	return result.Status, err
}

func (p *DotgoSMSMessageProvider) Send(ctx context.Context, tenantID, channel, recipient, content string) (string, error) {
	result, err := p.SendTracked(ctx, tenantID, channel, recipient, content)
	return result.Status, err
}

// SendTracked is Send that also returns the request id Dotgo was given. The
// delivery receipt Dotgo posts later carries that id back, and it is the only
// thing that ties the receipt to the message.
func (p *DotgoSMSMessageProvider) SendTracked(ctx context.Context, _ string, channel, recipient, content string) (domain.MessageSendResult, error) {
	failed := domain.MessageSendResult{Status: domain.StatusFailed}
	if strings.EqualFold(strings.TrimSpace(channel), "sms") == false && strings.TrimSpace(channel) != "" {
		return failed, fmt.Errorf("unsupported notification channel: %s", channel)
	}
	if strings.TrimSpace(recipient) == "" || strings.TrimSpace(content) == "" {
		return failed, errors.New("recipient and content are required")
	}
	if err := p.validateConfiguration(); err != nil {
		return failed, err
	}

	requestID, err := newDotgoRequestID()
	if err != nil {
		return failed, fmt.Errorf("generate dotgo request id: %w", err)
	}
	payload := map[string]any{
		"id":          requestID,
		"to":          []string{normalizeDotgoRecipient(recipient)},
		"body":        content,
		"sender_mask": p.senderMask,
	}
	if p.callbackURL != "" {
		payload["callback_url"] = p.callbackURL
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return failed, fmt.Errorf("marshal dotgo sms payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/v1/Accounts/%s/Messages", p.baseURL, url.PathEscape(p.accountID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return failed, fmt.Errorf("build dotgo sms request: %w", err)
	}
	// The account token goes in the header as issued, not as a Bearer token.
	req.Header.Set("Authorization", p.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return failed, fmt.Errorf("send dotgo sms: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return failed, fmt.Errorf("read dotgo sms response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return failed, fmt.Errorf("dotgo sms api %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	return domain.MessageSendResult{Status: domain.StatusSent, ProviderMessageID: requestID}, nil
}

func (p *MultiChannelMessageProvider) Send(ctx context.Context, tenantID, channel, recipient, content string) (string, error) {
	result, err := p.SendTracked(ctx, tenantID, channel, recipient, content)
	return result.Status, err
}

// SendTracked routes by channel like Send, and passes the SMS provider's request
// id through so its delivery receipts can be matched. The other channels report
// no id of their own here.
func (p *MultiChannelMessageProvider) SendTracked(ctx context.Context, tenantID, channel, recipient, content string) (domain.MessageSendResult, error) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "", "whatsapp":
		if p.whatsApp == nil {
			return domain.MessageSendResult{Status: domain.StatusFailed}, errors.New("whatsapp provider is not configured")
		}
		status, err := p.whatsApp.Send(ctx, tenantID, "whatsapp", recipient, content)
		return domain.MessageSendResult{Status: status}, err
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

	return p.send(ctx, phoneNumberID, accessToken, payload)
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

	return p.send(ctx, phoneNumberID, accessToken, payload)
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

	return p.send(ctx, phoneNumberID, accessToken, payload)
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

func (p *DotgoSMSMessageProvider) validateConfiguration() error {
	if p.baseURL == "" || p.accountID == "" || p.apiToken == "" || p.senderMask == "" {
		return errors.New("sms provider is not configured; set DOTGO_BASE_URL, DOTGO_TRANSACTIONAL_ACCOUNT_ID, DOTGO_TRANSACTIONAL_API_TOKEN, and DOTGO_TRANSACTIONAL_SENDER_MASK")
	}
	return nil
}

// normalizeDotgoRecipient puts a Nigerian number in the 234XXXXXXXXXX form
// Dotgo expects, with no plus sign: +2348012345678, 08012345678 and
// 8012345678 all become 2348012345678. Numbers outside that shape are passed
// through with the punctuation stripped, so another country's number is not
// mangled.
func normalizeDotgoRecipient(recipient string) string {
	var digits strings.Builder
	for _, r := range strings.TrimSpace(recipient) {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	normalized := digits.String()

	switch {
	case strings.HasPrefix(normalized, "0"):
		return "234" + normalized[1:]
	case !strings.HasPrefix(normalized, "234") && len(normalized) == 10:
		return "234" + normalized
	default:
		return normalized
	}
}

// DotgoCallbackURLWithToken returns the receipt URL to give Dotgo: the public
// address of this service's own receipt route with the shared secret added as
// its token, which the route checks. It returns "" when the address is not a
// usable URL or there is no secret, because a receipt this service would refuse
// is not worth asking for.
func DotgoCallbackURLWithToken(callbackURL, secret string) string {
	trimmedSecret := strings.TrimSpace(secret)
	if !ValidDotgoCallbackURL(callbackURL) || trimmedSecret == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(callbackURL))
	if err != nil {
		return ""
	}
	query := parsed.Query()
	query.Set("token", trimmedSecret)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// newDotgoRequestID is twelve random digits, the same shape fonu-api sends, so
// Dotgo sees one id format from both services and a receipt can be traced by it.
func newDotgoRequestID() (string, error) {
	digits := make([]byte, 12)
	for i := range digits {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		digits[i] = byte('0' + n.Int64())
	}
	return string(digits), nil
}

// ValidDotgoCallbackURL reports whether value can be sent to Dotgo as the
// delivery receipt URL: an absolute http(s) URL with no whitespace. A template
// left unfilled, such as "<fonu-api-base-url>/v1/...", fails this and must not
// reach the request.
func ValidDotgoCallbackURL(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n<>") {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
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
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err == nil && strings.TrimSpace(response.Error.Message) != "" {
		return fmt.Errorf("whatsapp api %d: %s", statusCode, response.Error.Message)
	}

	trimmedBody := strings.TrimSpace(string(body))
	if trimmedBody == "" {
		return fmt.Errorf("whatsapp api %d", statusCode)
	}

	return fmt.Errorf("whatsapp api %d: %s", statusCode, trimmedBody)
}
