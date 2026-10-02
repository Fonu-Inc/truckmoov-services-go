package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const mediaUploadTimeout = 60 * time.Second

// UploadMedia stores a file with Meta so it can be sent by id. The id is
// valid for 30 days.
func (p *MetaWhatsAppMessageProvider) UploadMedia(ctx context.Context, tenantID, filename, contentType string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("media file is empty")
	}
	phoneNumberID, accessToken, err := p.resolveCredentials(ctx, tenantID)
	if err != nil {
		return "", err
	}
	body, formType, err := mediaUploadForm(filename, contentType, data)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/%s/media", p.baseURL, phoneNumberID), body)
	if err != nil {
		return "", fmt.Errorf("build whatsapp media upload: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", formType)

	client := *p.client
	client.Timeout = mediaUploadTimeout
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload whatsapp media: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read whatsapp media upload: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiErr := buildWhatsAppAPIError(resp.StatusCode, payload)
		if typed, ok := domain.AsWhatsAppAPIError(apiErr); ok && typed.IsTokenInvalid() {
			p.markTokenInvalid(ctx, tenantID)
		}
		return "", apiErr
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &response); err != nil || strings.TrimSpace(response.ID) == "" {
		return "", errors.New("whatsapp media upload returned no id")
	}
	return strings.TrimSpace(response.ID), nil
}

func mediaUploadForm(filename, contentType string, data []byte) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("messaging_product", "whatsapp"); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("type", contentType); err != nil {
		return nil, "", err
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body, writer.FormDataContentType(), nil
}
