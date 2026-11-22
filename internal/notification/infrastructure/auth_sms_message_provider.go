package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

var (
	_ application.MessageProvider        = (*AuthSMSMessageProvider)(nil)
	_ application.TrackedMessageProvider = (*AuthSMSMessageProvider)(nil)
)

// maxAuthSMSResponseBytes bounds the auth response. It is a small JSON
// envelope, so this is generous and keeps a misbehaving dependency from
// allocating without limit.
const maxAuthSMSResponseBytes = 64 << 10

// AuthSMSMessageProvider sends SMS through the TS auth service's unified
// OTP/SMS endpoint. It is the only SMS path in the notification service now;
// auth owns the Dotgo credentials, dedup and provider call.
type AuthSMSMessageProvider struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
}

func NewAuthSMSMessageProvider(baseURL string, caller *identity.Caller, httpClient *http.Client) *AuthSMSMessageProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &AuthSMSMessageProvider{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		caller:     caller,
		httpClient: httpClient,
	}
}

func (p *AuthSMSMessageProvider) Send(ctx context.Context, tenantID, channel, recipient, content string) (string, error) {
	result, err := p.SendTracked(ctx, tenantID, channel, recipient, content)
	return result.Status, err
}

// SendTracked returns the reference auth's SMS service generated. The delivery
// receipt carries the same id, so it is the outbox message's provider id.
func (p *AuthSMSMessageProvider) SendTracked(ctx context.Context, tenantID, channel, recipient, content string) (domain.MessageSendResult, error) {
	failed := domain.MessageSendResult{Status: domain.StatusFailed}
	if strings.EqualFold(strings.TrimSpace(channel), "sms") == false && strings.TrimSpace(channel) != "" {
		return failed, fmt.Errorf("unsupported notification channel: %s", channel)
	}
	if strings.TrimSpace(recipient) == "" || strings.TrimSpace(content) == "" {
		return failed, fmt.Errorf("recipient and content are required")
	}
	if p.baseURL == "" || p.caller == nil {
		return failed, fmt.Errorf("auth SMS service is not configured")
	}

	payload, err := json.Marshal(map[string]string{
		"channel": "sms",
		"to":      recipient,
		"message": content,
	})
	if err != nil {
		return failed, fmt.Errorf("marshal SMS request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.baseURL+"/auth/internal/otp/send",
		bytes.NewReader(payload),
	)
	if err != nil {
		return failed, fmt.Errorf("build SMS request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if !p.caller.Authorize(req, "auth", identity.ActingFor{TenantID: tenantID, LegacyRole: "DISPATCHER"}) {
		return failed, fmt.Errorf("auth SMS service is not configured")
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return failed, fmt.Errorf("send SMS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAuthSMSResponseBytes))
	if err != nil {
		return failed, fmt.Errorf("read SMS response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		detail := strings.TrimSpace(string(body))
		if len(detail) > 300 {
			detail = detail[:300]
		}
		return failed, fmt.Errorf("auth SMS service %d: %s", resp.StatusCode, detail)
	}

	var envelope struct {
		Reference string `json:"reference"`
		Data      *struct {
			Reference string `json:"reference"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return failed, fmt.Errorf("decode SMS response: %w", err)
	}
	reference := envelope.Reference
	if envelope.Data != nil && envelope.Data.Reference != "" {
		reference = envelope.Data.Reference
	}
	if strings.TrimSpace(reference) == "" {
		return failed, fmt.Errorf("auth SMS service returned no reference")
	}

	return domain.MessageSendResult{
		Status:            domain.StatusSent,
		ProviderMessageID: reference,
	}, nil
}
