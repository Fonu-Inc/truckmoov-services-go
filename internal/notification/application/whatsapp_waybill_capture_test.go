package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeWhatsAppMediaDownloader struct {
	data        []byte
	contentType string
	err         error
	calls       []string // media ids asked for
}

func (d *fakeWhatsAppMediaDownloader) DownloadMedia(_ context.Context, _, mediaID string) ([]byte, string, error) {
	d.calls = append(d.calls, mediaID)
	if d.err != nil {
		return nil, "", d.err
	}
	return d.data, d.contentType, nil
}

type capturedWaybillCall struct {
	tenantID    string
	image       []byte
	filename    string
	contentType string
	senderPhone string
}

type fakeWaybillCaptureClient struct {
	err   error
	calls []capturedWaybillCall
}

func (c *fakeWaybillCaptureClient) CaptureWaybillFromWhatsApp(_ context.Context, tenantID string, image []byte, filename, contentType, senderPhone string) error {
	c.calls = append(c.calls, capturedWaybillCall{tenantID, image, filename, contentType, senderPhone})
	return c.err
}

func imageWebhookPayload(phoneNumberID, from, mediaID, mimeType string) []byte {
	return []byte(`{
		"entry": [{
			"changes": [{
				"value": {
					"metadata": {"phone_number_id": "` + phoneNumberID + `", "display_phone_number": "234700000"},
					"contacts": [{"profile": {"name": "Driver"}, "wa_id": "` + from + `"}],
					"messages": [{
						"id": "wamid.image1",
						"from": "` + from + `",
						"timestamp": "1700000000",
						"type": "image",
						"image": {"id": "` + mediaID + `", "mime_type": "` + mimeType + `"}
					}]
				}
			}]
		}]
	}`)
}

func textWebhookPayload(phoneNumberID, from, body string) []byte {
	return []byte(`{
		"entry": [{
			"changes": [{
				"value": {
					"metadata": {"phone_number_id": "` + phoneNumberID + `", "display_phone_number": "234700000"},
					"contacts": [{"profile": {"name": "Driver"}, "wa_id": "` + from + `"}],
					"messages": [{
						"id": "wamid.text1",
						"from": "` + from + `",
						"timestamp": "1700000000",
						"type": "text",
						"text": {"body": "` + body + `"}
					}]
				}
			}]
		}]
	}`)
}

func newTestWebhookUseCase() (*HandleWhatsAppWebhookUseCase, *fakeWhatsAppConversationRepository) {
	repository := newFakeWhatsAppConversationRepository()
	// The webhook only ever attaches an inbound message to a conversation that
	// already exists for that tenant and phone number.
	if _, err := repository.SaveConversation(context.Background(), domain.WhatsAppConversation{
		ID:          "conv-1",
		TenantID:    "tenant-a",
		DriverPhone: "2348000000001",
		CreatedAt:   time.Unix(1_700_000_000, 0).UTC(),
		UpdatedAt:   time.Unix(1_700_000_000, 0).UTC(),
	}); err != nil {
		panic(err)
	}
	accounts := &fixedWhatsAppAccountRepository{
		account: domain.WhatsAppAccount{TenantID: "tenant-a", PhoneNumberID: "pn-a"},
	}
	useCase := NewHandleWhatsAppWebhookUseCase(
		repository,
		nil,
		accounts,
		nil,
		nil,
		nil,
		nil,
		fixedNotificationClock{now: time.Unix(1_700_000_100, 0).UTC()},
		fixedNotificationIDGenerator{id: "1"},
	)
	return useCase, repository
}

func TestHandleWhatsAppWebhookUseCase_CapturesWaybillFromInboundImage(t *testing.T) {
	useCase, _ := newTestWebhookUseCase()
	downloader := &fakeWhatsAppMediaDownloader{data: []byte("jpeg-bytes"), contentType: "image/jpeg"}
	client := &fakeWaybillCaptureClient{}
	useCase = useCase.WithWaybillCapture(downloader, client)

	processed, err := useCase.Execute(context.Background(), HandleWhatsAppWebhookCommand{
		Payload: imageWebhookPayload("pn-a", "2348000000001", "media-1", "image/jpeg"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 processed, got %d", processed)
	}

	if len(downloader.calls) != 1 || downloader.calls[0] != "media-1" {
		t.Fatalf("expected media-1 downloaded, got %v", downloader.calls)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one waybill capture call, got %d", len(client.calls))
	}
	call := client.calls[0]
	if call.tenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %s", call.tenantID)
	}
	if string(call.image) != "jpeg-bytes" {
		t.Fatalf("expected downloaded bytes forwarded, got %q", call.image)
	}
	if call.senderPhone != "2348000000001" {
		t.Fatalf("expected sender phone forwarded, got %s", call.senderPhone)
	}
	if call.contentType != "image/jpeg" {
		t.Fatalf("expected content type forwarded, got %s", call.contentType)
	}
}

func TestHandleWhatsAppWebhookUseCase_DoesNotCaptureWaybillForTextMessage(t *testing.T) {
	useCase, _ := newTestWebhookUseCase()
	downloader := &fakeWhatsAppMediaDownloader{}
	client := &fakeWaybillCaptureClient{}
	useCase = useCase.WithWaybillCapture(downloader, client)

	_, err := useCase.Execute(context.Background(), HandleWhatsAppWebhookCommand{
		Payload: textWebhookPayload("pn-a", "2348000000001", "hello"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(client.calls) != 0 {
		t.Fatalf("expected no waybill capture for a text message, got %d calls", len(client.calls))
	}
}

func TestHandleWhatsAppWebhookUseCase_WithoutWaybillCaptureWiredSkipsSilently(t *testing.T) {
	useCase, _ := newTestWebhookUseCase()

	processed, err := useCase.Execute(context.Background(), HandleWhatsAppWebhookCommand{
		Payload: imageWebhookPayload("pn-a", "2348000000001", "media-1", "image/jpeg"),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected the message itself to still be recorded, got %d", processed)
	}
}

func TestHandleWhatsAppWebhookUseCase_WaybillCaptureFailureDoesNotFailTheWebhook(t *testing.T) {
	useCase, _ := newTestWebhookUseCase()
	downloader := &fakeWhatsAppMediaDownloader{err: errors.New("meta unavailable")}
	client := &fakeWaybillCaptureClient{}
	useCase = useCase.WithWaybillCapture(downloader, client)

	processed, err := useCase.Execute(context.Background(), HandleWhatsAppWebhookCommand{
		Payload: imageWebhookPayload("pn-a", "2348000000001", "media-1", "image/jpeg"),
	})
	if err != nil {
		t.Fatalf("expected the webhook itself to still succeed, got %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 processed, got %d", processed)
	}
	if len(client.calls) != 0 {
		t.Fatalf("expected no capture attempt once the download failed, got %d", len(client.calls))
	}
}

func TestHandleWhatsAppWebhookUseCase_FleetFailureDoesNotFailTheWebhook(t *testing.T) {
	useCase, _ := newTestWebhookUseCase()
	downloader := &fakeWhatsAppMediaDownloader{data: []byte("jpeg-bytes"), contentType: "image/jpeg"}
	client := &fakeWaybillCaptureClient{err: errors.New("fleet unreachable")}
	useCase = useCase.WithWaybillCapture(downloader, client)

	processed, err := useCase.Execute(context.Background(), HandleWhatsAppWebhookCommand{
		Payload: imageWebhookPayload("pn-a", "2348000000001", "media-1", "image/jpeg"),
	})
	if err != nil {
		t.Fatalf("expected the webhook itself to still succeed, got %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 processed, got %d", processed)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected fleet to still be called once, got %d", len(client.calls))
	}
}
