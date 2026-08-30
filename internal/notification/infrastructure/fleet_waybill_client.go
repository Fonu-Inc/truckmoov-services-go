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
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// FleetWaybillClient hands a downloaded WhatsApp image to fleet's waybill
// capture endpoint. Fleet resolves the sender phone to a driver itself, so
// this carries only the phone number, not a driver id.
type FleetWaybillClient struct {
	baseURL    string
	jwtSecret  string
	httpClient *http.Client
	now        func() time.Time
}

type fleetWaybillServiceTokenPayload struct {
	Subject  string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenantId"`
	Expiry   int64  `json:"exp"`
}

func NewFleetWaybillClient(baseURL, jwtSecret string, client *http.Client) *FleetWaybillClient {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &FleetWaybillClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		jwtSecret:  strings.TrimSpace(jwtSecret),
		httpClient: client,
		now:        time.Now,
	}
}

func (c *FleetWaybillClient) CaptureWaybillFromWhatsApp(ctx context.Context, tenantID string, image []byte, filename, contentType, senderPhone string) error {
	if c.baseURL == "" {
		return fmt.Errorf("fleet service url is not configured")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("build waybill form file: %w", err)
	}
	if _, err := part.Write(image); err != nil {
		return fmt.Errorf("write waybill image: %w", err)
	}
	if err := writer.WriteField("replyAddress", senderPhone); err != nil {
		return fmt.Errorf("write waybill reply address: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close waybill form: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/waybills/upload?source=whatsapp", &body)
	if err != nil {
		return fmt.Errorf("build waybill request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if token := c.buildInternalToken(tenantID); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call fleet service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fleet service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (c *FleetWaybillClient) buildInternalToken(tenantID string) string {
	if strings.TrimSpace(c.jwtSecret) == "" || strings.TrimSpace(tenantID) == "" {
		return ""
	}
	payload := fleetWaybillServiceTokenPayload{
		Subject:  "notification-service",
		Email:    "notification-service@truckmoov.local",
		Role:     "DISPATCHER",
		TenantID: strings.TrimSpace(tenantID),
		Expiry:   c.now().Add(5 * time.Minute).Unix(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	mac := hmac.New(sha256.New, []byte(c.jwtSecret))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature
}
