package trackinghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/internal/tracking/domain"
)

type fakeTrackingPublisher struct{}

func (f *fakeTrackingPublisher) PublishLocationUpdate(_ context.Context, _ domain.LocationUpdate) error {
	return nil
}

func (f *fakeTrackingPublisher) PublishAlertTriggered(_ context.Context, _ domain.AlertTriggered) error {
	return nil
}

type fakeLocationRepo struct{}

func (f *fakeLocationRepo) Save(_ application.LatestLocation) error {
	return nil
}

type fixedTrackingClock struct{ now time.Time }

func (f fixedTrackingClock) Now() time.Time { return f.now }

type fixedTrackingID struct{ value string }

func (f fixedTrackingID) New(prefix string) string { return prefix + "-" + f.value }

func TestHandler_IngestContract(t *testing.T) {
	clock := fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)}
	id := fixedTrackingID{value: "1"}
	publisher := &fakeTrackingPublisher{}

	ingestUseCase := application.NewIngestLocationUseCase(
		publisher,
		&fakeLocationRepo{},
		clock,
		id,
		"default",
	)
	alertUseCase := application.NewPublishAlertUseCase(publisher, clock, id, "default")
	handler := NewHandler(ingestUseCase, alertUseCase)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{"vehicle_id":"vehicle-1","device_id":"device-1","lat":6.5,"lng":3.4}`)
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["published"] != true {
		t.Fatalf("expected published=true")
	}
}

func TestHandler_WebsocketPlaceholderContract(t *testing.T) {
	clock := fixedTrackingClock{now: time.Now()}
	id := fixedTrackingID{value: "2"}
	publisher := &fakeTrackingPublisher{}

	ingestUseCase := application.NewIngestLocationUseCase(
		publisher,
		&fakeLocationRepo{},
		clock,
		id,
		"default",
	)
	alertUseCase := application.NewPublishAlertUseCase(publisher, clock, id, "default")
	handler := NewHandler(ingestUseCase, alertUseCase)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/tracking/ws", nil)
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusNotImplemented {
		t.Fatalf("expected status %d, got %d", http.StatusNotImplemented, res.Code)
	}
}
