package notificationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
)

const smsToken = "callback-secret"

type smsWebhookFixture struct {
	handler *Handler
	outbox  *infrastructure.InMemoryOutboxRepository
	log     *infrastructure.InMemoryDeliveryLogRepository
}

func newSMSWebhookFixture(t *testing.T) smsWebhookFixture {
	t.Helper()

	outbox := infrastructure.NewInMemoryOutboxRepository()
	log := infrastructure.NewInMemoryDeliveryLogRepository()
	clock := &mutableNotificationClock{now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	useCase := application.NewHandleSMSDeliveryReceiptUseCase(outbox, log, &fakeNotificationPublisher{}, clock, &sequentialNotificationID{})

	message, err := domain.NewOutboxMessage(domain.OutboxMessage{
		ID: "msg-1", TenantID: "tenant-a", Channel: "sms", Recipient: "+2348012345678", Content: "hello",
		Status: domain.StatusSent, ProviderMessageID: "482910375561", CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	if err != nil {
		t.Fatalf("build outbox message: %v", err)
	}
	if err := outbox.Save(message); err != nil {
		t.Fatalf("save outbox message: %v", err)
	}
	entry, err := domain.NewDeliveryLogEntry(domain.DeliveryLogEntry{
		ID: "log-1", TenantID: "tenant-a", Channel: "sms", Recipient: "+2348012345678", Content: "hello",
		Status: domain.StatusSent, ProviderMessageID: "482910375561", CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	if err != nil {
		t.Fatalf("build log entry: %v", err)
	}
	if err := log.Append(context.Background(), entry); err != nil {
		t.Fatalf("append log entry: %v", err)
	}

	return smsWebhookFixture{handler: (&Handler{}).WithSMSReceipts(useCase, smsToken), outbox: outbox, log: log}
}

func (f smsWebhookFixture) post(method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	res := httptest.NewRecorder()
	f.handler.smsWebhook(res, req)
	return res
}

func (f smsWebhookFixture) outboxStatus(t *testing.T) string {
	t.Helper()
	message, ok := f.outbox.FindByProviderMessageID("482910375561")
	if !ok {
		t.Fatal("outbox message not found")
	}
	return message.Status
}

func TestSMSWebhookRecordsADeliveredReceipt(t *testing.T) {
	f := newSMSWebhookFixture(t)

	res := f.post(http.MethodPost, "/notify/sms/webhook?token="+smsToken, `{"id":"482910375561","status":"delivered"}`)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if got := f.outboxStatus(t); got != domain.StatusDelivered {
		t.Fatalf("expected the message delivered, got %s", got)
	}
}

func TestSMSWebhookAcceptsTheBodyShapesDotgoSends(t *testing.T) {
	bodies := map[string]string{
		"a list":              `[{"id":"482910375561","status":"delivered"}]`,
		"a numeric id":        `{"id":482910375561,"status":"delivered"}`,
		"wrapped in a string": `"{\"id\":\"482910375561\",\"status\":\"delivered\"}"`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := newSMSWebhookFixture(t)

			res := f.post(http.MethodPost, "/notify/sms/webhook?token="+smsToken, body)

			if res.Code != http.StatusOK || f.outboxStatus(t) != domain.StatusDelivered {
				t.Fatalf("expected the receipt applied, got %d status %s", res.Code, f.outboxStatus(t))
			}
		})
	}
}

// Dotgo does not sign its callbacks, so the token in the URL is the only thing
// standing between a stranger and the delivery status of real messages.
func TestSMSWebhookRefusesARequestWithoutTheToken(t *testing.T) {
	targets := map[string]string{
		"no token":    "/notify/sms/webhook",
		"empty token": "/notify/sms/webhook?token=",
		"wrong token": "/notify/sms/webhook?token=guess",
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			f := newSMSWebhookFixture(t)

			res := f.post(http.MethodPost, target, `{"id":"482910375561","status":"failed"}`)

			if res.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", res.Code)
			}
			if got := f.outboxStatus(t); got != domain.StatusSent {
				t.Fatalf("a refused request must not change the message, got %s", got)
			}
		})
	}
}

func TestSMSWebhookIsClosedWhenNoTokenIsConfigured(t *testing.T) {
	f := newSMSWebhookFixture(t)
	f.handler.smsWebhookToken = ""

	res := f.post(http.MethodPost, "/notify/sms/webhook?token=", `{"id":"482910375561","status":"delivered"}`)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", res.Code)
	}
}

func TestSMSWebhookAnswers200ForAMessageItNeverSent(t *testing.T) {
	f := newSMSWebhookFixture(t)

	res := f.post(http.MethodPost, "/notify/sms/webhook?token="+smsToken, `{"id":"999999999999","status":"delivered"}`)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 so Dotgo does not retry, got %d", res.Code)
	}
	if got := f.outboxStatus(t); got != domain.StatusSent {
		t.Fatalf("expected the other message untouched, got %s", got)
	}
}

func TestSMSWebhookRejectsWhatItCannotRead(t *testing.T) {
	f := newSMSWebhookFixture(t)

	if res := f.post(http.MethodPost, "/notify/sms/webhook?token="+smsToken, `not json`); res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
	if res := f.post(http.MethodGet, "/notify/sms/webhook?token="+smsToken, ``); res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.Code)
	}
}

func TestSMSWebhookIsPublicInTheRouteTable(t *testing.T) {
	handler, _, _, _, _, _, _ := notificationHandlerForTests(t)

	// No bearer token: the route is reached by Dotgo, and the token in the
	// query is what it is checked with. It must not be swallowed by auth.
	req := httptest.NewRequest(http.MethodPost, "/notify/sms/webhook?token=anything", strings.NewReader(`{}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code == http.StatusNotFound || res.Code == http.StatusForbidden {
		t.Fatalf("expected the route to be reachable without a bearer token, got %d", res.Code)
	}
}
