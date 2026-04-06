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

	"github.com/truckmoov/services-go/internal/dispatch/application"
)

type NotificationJobInstructionSender struct {
	baseURL   string
	jwtSecret string
	client    *http.Client
	now       func() time.Time
}

type internalServiceTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
}

type sendJobInstructionRequest struct {
	JobID            string                                `json:"jobId,omitempty"`
	DriverID         string                                `json:"driverId,omitempty"`
	Recipient        string                                `json:"recipient"`
	TemplateName     string                                `json:"templateName"`
	Language         string                                `json:"language,omitempty"`
	DriverName       string                                `json:"driverName"`
	JobReference     string                                `json:"jobReference"`
	PickupLocation   string                                `json:"pickupLocation"`
	DeliveryLocation string                                `json:"deliveryLocation"`
	CargoDescription string                                `json:"cargoDescription"`
	Priority         string                                `json:"priority"`
	HeaderMedia      *sendJobInstructionHeaderMediaRequest `json:"headerMedia,omitempty"`
	ButtonParameters []sendJobInstructionButtonParameter   `json:"buttonParameters,omitempty"`
}

type sendJobInstructionHeaderMediaRequest struct {
	Type     string `json:"type,omitempty"`
	Link     string `json:"link,omitempty"`
	ID       string `json:"id,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type sendJobInstructionButtonParameter struct {
	Index   int    `json:"index"`
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Payload string `json:"payload,omitempty"`
}

func NewNotificationJobInstructionSender(baseURL, jwtSecret string, client *http.Client) *NotificationJobInstructionSender {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &NotificationJobInstructionSender{
		baseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret: strings.TrimSpace(jwtSecret),
		client:    client,
		now:       time.Now,
	}
}

func (s *NotificationJobInstructionSender) SendJobInstruction(ctx context.Context, payload application.DispatchJobInstructionPayload) error {
	requestBody := sendJobInstructionRequest{
		JobID:            strings.TrimSpace(payload.JobID),
		DriverID:         strings.TrimSpace(payload.DriverID),
		Recipient:        strings.TrimSpace(payload.Recipient),
		TemplateName:     strings.TrimSpace(payload.TemplateName),
		Language:         strings.TrimSpace(payload.Language),
		DriverName:       strings.TrimSpace(payload.DriverName),
		JobReference:     strings.TrimSpace(payload.JobReference),
		PickupLocation:   strings.TrimSpace(payload.PickupLocation),
		DeliveryLocation: strings.TrimSpace(payload.DeliveryLocation),
		CargoDescription: strings.TrimSpace(payload.CargoDescription),
		Priority:         strings.TrimSpace(payload.Priority),
		HeaderMedia:      normalizeHeaderMedia(payload.HeaderMedia),
		ButtonParameters: normalizeButtonParameters(payload.ButtonParameters),
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal notification request: %w", err)
	}

	endpoint := s.baseURL + "/notify/whatsapp/send-job-instruction"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token := s.buildInternalToken(strings.TrimSpace(payload.TenantID)); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send notification request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	message := extractErrorMessage(responseBody)
	if message == "" {
		message = strings.TrimSpace(string(responseBody))
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("notification service returned %d: %s", resp.StatusCode, message)
}

func (s *NotificationJobInstructionSender) buildInternalToken(tenantID string) string {
	if strings.TrimSpace(s.jwtSecret) == "" || strings.TrimSpace(tenantID) == "" {
		return ""
	}
	payload := internalServiceTokenPayload{
		Subject:  "dispatch-service",
		Email:    "dispatch-service@truckmoov.local",
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

func normalizeHeaderMedia(media *application.JobInstructionHeaderMedia) *sendJobInstructionHeaderMediaRequest {
	if media == nil {
		return nil
	}
	return &sendJobInstructionHeaderMediaRequest{
		Type:     strings.TrimSpace(media.Type),
		Link:     strings.TrimSpace(media.Link),
		ID:       strings.TrimSpace(media.ID),
		Filename: strings.TrimSpace(media.Filename),
	}
}

func normalizeButtonParameters(parameters []application.JobInstructionButtonParameter) []sendJobInstructionButtonParameter {
	if len(parameters) == 0 {
		return nil
	}
	normalized := make([]sendJobInstructionButtonParameter, 0, len(parameters))
	for _, parameter := range parameters {
		normalized = append(normalized, sendJobInstructionButtonParameter{
			Index:   parameter.Index,
			Type:    strings.TrimSpace(parameter.Type),
			Text:    strings.TrimSpace(parameter.Text),
			Payload: strings.TrimSpace(parameter.Payload),
		})
	}
	return normalized
}

func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
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
