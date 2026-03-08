package notificationhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
)

type fixedNotificationClock struct{ now time.Time }

func (f fixedNotificationClock) Now() time.Time { return f.now }

type fixedNotificationID struct{ value string }

func (f fixedNotificationID) New(prefix string) string { return prefix + "-" + f.value }

type fakeNotificationProvider struct {
	status string
	err    error
}

func (f *fakeNotificationProvider) Send(_ context.Context, _, _, _ string) (string, error) {
	return f.status, f.err
}

type fakeNotificationPublisher struct{}

func (f *fakeNotificationPublisher) PublishDeliveryUpdated(_ context.Context, _ domain.MessageDeliveryUpdated) error {
	return nil
}

func TestHandler_SendMessageContract(t *testing.T) {
	repository := infrastructure.NewInMemoryOutboxRepository()
	provider := &fakeNotificationProvider{status: "sent"}
	publisher := &fakeNotificationPublisher{}
	clock := fixedNotificationClock{now: time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC)}
	id := fixedNotificationID{value: "1"}

	sendUseCase := application.NewSendMessageUseCase(
		repository,
		provider,
		publisher,
		clock,
		id,
		"default",
	)
	retryUseCase := application.NewRetryMessageUseCase(
		repository,
		provider,
		publisher,
		clock,
		id,
	)

	handler := NewHandler(sendUseCase, retryUseCase)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{"recipient":"+2340000","content":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/notify/messages/send", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}
}

func TestHandler_RetryMissingMessageContract(t *testing.T) {
	repository := infrastructure.NewInMemoryOutboxRepository()
	provider := &fakeNotificationProvider{status: "sent"}
	publisher := &fakeNotificationPublisher{}
	clock := fixedNotificationClock{now: time.Now()}
	id := fixedNotificationID{value: "2"}

	sendUseCase := application.NewSendMessageUseCase(
		repository,
		provider,
		publisher,
		clock,
		id,
		"default",
	)
	retryUseCase := application.NewRetryMessageUseCase(
		repository,
		provider,
		publisher,
		clock,
		id,
	)

	handler := NewHandler(sendUseCase, retryUseCase)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/notify/messages/retry/missing", nil)
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, res.Code)
	}
}
