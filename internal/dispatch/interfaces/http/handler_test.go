package dispatchhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	trackinggrpc "github.com/truckmoov/services-go/pkg/grpc"
)

type fakeJobStatusPublisher struct{}

func (f *fakeJobStatusPublisher) PublishJobStatusChanged(_ context.Context, _ domain.JobStatusChanged) error {
	return nil
}

type fixedDispatchClock struct{ now time.Time }

func (f fixedDispatchClock) Now() time.Time { return f.now }

type fixedDispatchID struct{ id string }

func (f fixedDispatchID) New() string { return f.id }

func TestHandler_UpdateStatusContract(t *testing.T) {
	useCase := application.NewPublishJobStatusChangedUseCase(
		&fakeJobStatusPublisher{},
		fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		fixedDispatchID{id: "evt-1"},
		"default",
	)
	handler := NewHandler(useCase, trackinggrpc.NewStubTrackingClient())

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{"job_id":"job-1","new_status":"assigned"}`)
	req := httptest.NewRequest(http.MethodPost, "/dispatch/jobs/status", body)
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

func TestHandler_ETAContract(t *testing.T) {
	useCase := application.NewPublishJobStatusChangedUseCase(
		&fakeJobStatusPublisher{},
		fixedDispatchClock{now: time.Now()},
		fixedDispatchID{id: "evt-2"},
		"default",
	)
	handler := NewHandler(useCase, trackinggrpc.NewStubTrackingClient())

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/dispatch/eta?job_id=job-1&vehicle_id=vehicle-1&destination=dest", nil)
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
}
