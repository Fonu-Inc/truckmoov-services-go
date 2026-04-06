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
	baseURL           string
	businessAccountID string
	accessToken       string
	client            *http.Client
}

func NewMetaWhatsAppTemplateProvider(baseURL, businessAccountID, accessToken string) *MetaWhatsAppTemplateProvider {
	return NewMetaWhatsAppTemplateProviderWithClient(baseURL, businessAccountID, accessToken, nil)
}

func NewMetaWhatsAppTemplateProviderWithClient(baseURL, businessAccountID, accessToken string, client *http.Client) *MetaWhatsAppTemplateProvider {
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBaseURL == "" {
		trimmedBaseURL = defaultWhatsAppAPIBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	return &MetaWhatsAppTemplateProvider{
		baseURL:           trimmedBaseURL,
		businessAccountID: strings.TrimSpace(businessAccountID),
		accessToken:       strings.TrimSpace(accessToken),
		client:            client,
	}
}

func (p *MetaWhatsAppTemplateProvider) CreateTemplate(ctx context.Context, request domain.WhatsAppTemplateCreateRequest) (domain.WhatsAppTemplateCreateResponse, error) {
	if err := p.validateConfiguration(); err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, err
	}

	body, err := json.Marshal(request)
	if err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, fmt.Errorf("marshal whatsapp template request: %w", err)
	}

	apiRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/%s/message_templates", p.baseURL, p.businessAccountID), bytes.NewReader(body))
	if err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, fmt.Errorf("build whatsapp template create request: %w", err)
	}
	apiRequest.Header.Set("Authorization", "Bearer "+p.accessToken)
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
	if err := p.validateConfiguration(); err != nil {
		return domain.WhatsAppTemplateListResponse{}, err
	}

	query := url.Values{}
	if request.Limit > 0 {
		query.Set("limit", strconv.Itoa(request.Limit))
	}
	if strings.TrimSpace(request.After) != "" {
		query.Set("after", strings.TrimSpace(request.After))
	}

	endpoint := fmt.Sprintf("%s/%s/message_templates", p.baseURL, p.businessAccountID)
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	apiRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return domain.WhatsAppTemplateListResponse{}, fmt.Errorf("build whatsapp template list request: %w", err)
	}
	apiRequest.Header.Set("Authorization", "Bearer "+p.accessToken)

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

func (p *MetaWhatsAppTemplateProvider) validateConfiguration() error {
	if p.accessToken == "" || p.businessAccountID == "" {
		return errors.New("whatsapp template provider is not configured; set WHATSAPP_ACCESS_TOKEN and WHATSAPP_BUSINESS_ACCOUNT_ID")
	}
	return nil
}
