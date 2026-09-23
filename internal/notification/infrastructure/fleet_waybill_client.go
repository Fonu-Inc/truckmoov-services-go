package infrastructure

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/pkg/identity"
)

// FleetWaybillClient hands a downloaded WhatsApp image to fleet's waybill
// capture endpoint. Fleet resolves the sender phone to a driver itself, so
// this carries only the phone number, not a driver id.
type FleetWaybillClient struct {
	baseURL    string
	caller     *identity.Caller
	httpClient *http.Client
	now        func() time.Time
}

func NewFleetWaybillClient(baseURL string, caller *identity.Caller, client *http.Client) *FleetWaybillClient {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &FleetWaybillClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		caller:     caller,
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/fleet/waybills/upload?source=whatsapp", &body)
	if err != nil {
		return fmt.Errorf("build waybill request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	c.caller.Authorize(req, "fleet", identity.ActingFor{TenantID: tenantID, LegacyRole: "DISPATCHER"})

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
