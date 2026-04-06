package infrastructure

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
)

type DispatchDriverStatusRequestSender struct {
	baseURL   string
	jwtSecret string
	client    *http.Client
	now       func() time.Time
}

type createDriverStatusRequestBody struct {
	DriverID        string `json:"driver_id"`
	DriverPhone     string `json:"driver_phone,omitempty"`
	Keyword         string `json:"keyword"`
	RawMessage      string `json:"raw_message,omitempty"`
	SourceMessageID string `json:"source_message_id,omitempty"`
	Channel         string `json:"channel,omitempty"`
}

type createDriverStatusRequestResponseEnvelope struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Request struct {
			ID              string `json:"id"`
			RequestedStatus string `json:"requested_status"`
		} `json:"request"`
	} `json:"data"`
}

type notificationInternalServiceTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
}

func NewDispatchDriverStatusRequestSender(baseURL, jwtSecret string, client *http.Client) *DispatchDriverStatusRequestSender {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &DispatchDriverStatusRequestSender{
		baseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret: strings.TrimSpace(jwtSecret),
		client:    client,
		now:       time.Now,
	}
}

func (s *DispatchDriverStatusRequestSender) CreateDriverStatusRequest(ctx context.Context, command application.CreateDriverStatusRequestFromWhatsAppCommand) (application.DriverStatusRequestReceipt, error) {
	body, err := json.Marshal(createDriverStatusRequestBody{
		DriverID:        strings.TrimSpace(command.DriverID),
		DriverPhone:     strings.TrimSpace(command.DriverPhone),
		Keyword:         strings.TrimSpace(command.Keyword),
		RawMessage:      strings.TrimSpace(command.RawMessage),
		SourceMessageID: strings.TrimSpace(command.SourceMessageID),
		Channel:         strings.TrimSpace(command.Channel),
	})
	if err != nil {
		return application.DriverStatusRequestReceipt{}, fmt.Errorf("marshal driver status request: %w", err)
	}

	endpoint := s.baseURL + "/dispatch/jobs/" + strings.TrimSpace(command.JobID) + "/driver-status-requests"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return application.DriverStatusRequestReceipt{}, fmt.Errorf("build dispatch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token := s.buildInternalToken(strings.TrimSpace(command.TenantID)); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return application.DriverStatusRequestReceipt{}, fmt.Errorf("send dispatch request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := extractDispatchErrorMessage(responseBody)
		if message == "" {
			message = strings.TrimSpace(string(responseBody))
		}
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return application.DriverStatusRequestReceipt{}, fmt.Errorf("dispatch service returned %d: %s", resp.StatusCode, message)
	}

	var envelope createDriverStatusRequestResponseEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return application.DriverStatusRequestReceipt{}, fmt.Errorf("decode dispatch response: %w", err)
	}

	return application.DriverStatusRequestReceipt{
		RequestID:       strings.TrimSpace(envelope.Data.Request.ID),
		RequestedStatus: strings.TrimSpace(envelope.Data.Request.RequestedStatus),
	}, nil
}

func (s *DispatchDriverStatusRequestSender) buildInternalToken(tenantID string) string {
	if strings.TrimSpace(s.jwtSecret) == "" || strings.TrimSpace(tenantID) == "" {
		return ""
	}
	payload := notificationInternalServiceTokenPayload{
		Subject:  "notification-service",
		Email:    "notification-service@truckmoov.local",
		Role:     "DISPATCHER",
		TenantID: strings.TrimSpace(tenantID),
		Expiry:   s.now().Add(5 * time.Minute).Unix(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	mac := hmac.New(sha256.New, []byte(s.jwtSecret))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature
}

func extractDispatchErrorMessage(body []byte) string {
	var envelope struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	if strings.TrimSpace(envelope.Message) != "" {
		return strings.TrimSpace(envelope.Message)
	}
	return strings.TrimSpace(envelope.Error)
}
