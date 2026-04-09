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
	"strconv"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type MetaWhatsAppTemplateProvider struct {
	baseURL                 string
	accountRepository       whatsAppAccountRepository
	tokenCipher             whatsAppTokenCipher
	legacyBusinessAccountID string
	legacyAccessToken       string
	client                  *http.Client
}

func NewMetaWhatsAppTemplateProvider(baseURL string, accountRepository whatsAppAccountRepository, tokenCipher whatsAppTokenCipher, legacyBusinessAccountID, legacyAccessToken string) *MetaWhatsAppTemplateProvider {
	return NewMetaWhatsAppTemplateProviderWithClient(baseURL, accountRepository, tokenCipher, legacyBusinessAccountID, legacyAccessToken, nil)
}

func NewMetaWhatsAppTemplateProviderWithClient(baseURL string, accountRepository whatsAppAccountRepository, tokenCipher whatsAppTokenCipher, legacyBusinessAccountID, legacyAccessToken string, client *http.Client) *MetaWhatsAppTemplateProvider {
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBaseURL == "" {
		trimmedBaseURL = defaultWhatsAppAPIBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	return &MetaWhatsAppTemplateProvider{
		baseURL:                 trimmedBaseURL,
		accountRepository:       accountRepository,
		tokenCipher:             tokenCipher,
		legacyBusinessAccountID: strings.TrimSpace(legacyBusinessAccountID),
		legacyAccessToken:       strings.TrimSpace(legacyAccessToken),
		client:                  client,
	}
}

func (p *MetaWhatsAppTemplateProvider) CreateTemplate(ctx context.Context, request domain.WhatsAppTemplateCreateRequest) (domain.WhatsAppTemplateCreateResponse, error) {
	businessAccountID, accessToken, err := p.resolveCredentials(ctx, request.TenantID)
	if err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, err
	}

	apiRequestBody := domain.WhatsAppTemplateCreateRequest{
		Name:                request.Name,
		Language:            request.Language,
		Category:            request.Category,
		AllowCategoryChange: request.AllowCategoryChange,
		ParameterFormat:     request.ParameterFormat,
		Components:          request.Components,
	}

	body, err := json.Marshal(apiRequestBody)
	if err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, fmt.Errorf("marshal whatsapp template request: %w", err)
	}

	apiRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/%s/message_templates", p.baseURL, businessAccountID), bytes.NewReader(body))
	if err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, fmt.Errorf("build whatsapp template create request: %w", err)
	}
	apiRequest.Header.Set("Authorization", "Bearer "+accessToken)
	apiRequest.Header.Set("Content-Type", "application/json")

	response, err := p.client.Do(apiRequest)
	if err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, fmt.Errorf("create whatsapp template: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, fmt.Errorf("read whatsapp template create response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return domain.WhatsAppTemplateCreateResponse{}, buildWhatsAppAPIError(response.StatusCode, responseBody)
	}

	var result domain.WhatsAppTemplateCreateResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, fmt.Errorf("decode whatsapp template create response: %w", err)
	}
	return result, nil
}

func (p *MetaWhatsAppTemplateProvider) ListTemplates(ctx context.Context, request domain.WhatsAppTemplateListRequest) (domain.WhatsAppTemplateListResponse, error) {
	businessAccountID, accessToken, err := p.resolveCredentials(ctx, request.TenantID)
	if err != nil {
		return domain.WhatsAppTemplateListResponse{}, err
	}

	query := url.Values{}
	if request.Limit > 0 {
		query.Set("limit", strconv.Itoa(request.Limit))
	}
	if strings.TrimSpace(request.After) != "" {
		query.Set("after", strings.TrimSpace(request.After))
	}

	endpoint := fmt.Sprintf("%s/%s/message_templates", p.baseURL, businessAccountID)
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	apiRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return domain.WhatsAppTemplateListResponse{}, fmt.Errorf("build whatsapp template list request: %w", err)
	}
	apiRequest.Header.Set("Authorization", "Bearer "+accessToken)

	response, err := p.client.Do(apiRequest)
	if err != nil {
		return domain.WhatsAppTemplateListResponse{}, fmt.Errorf("list whatsapp templates: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return domain.WhatsAppTemplateListResponse{}, fmt.Errorf("read whatsapp template list response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return domain.WhatsAppTemplateListResponse{}, buildWhatsAppAPIError(response.StatusCode, responseBody)
	}

	var result domain.WhatsAppTemplateListResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return domain.WhatsAppTemplateListResponse{}, fmt.Errorf("decode whatsapp template list response: %w", err)
	}
	if result.Data == nil {
		result.Data = []domain.WhatsAppTemplate{}
	}
	return result, nil
}

func (p *MetaWhatsAppTemplateProvider) resolveCredentials(ctx context.Context, tenantID string) (string, string, error) {
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
			if strings.TrimSpace(account.BusinessAccountID) == "" || strings.TrimSpace(accessToken) == "" {
				return "", "", errors.New("whatsapp account is incomplete for this tenant")
			}
			return strings.TrimSpace(account.BusinessAccountID), strings.TrimSpace(accessToken), nil
		}
	}
	if p.legacyAccessToken != "" && p.legacyBusinessAccountID != "" {
		return p.legacyBusinessAccountID, p.legacyAccessToken, nil
	}
	return "", "", errors.New("whatsapp account is not connected for this tenant")
}
