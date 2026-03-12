package dispatchhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/internal/dispatch/infrastructure"
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

type sequenceDispatchID struct {
	next int
}

func (s *sequenceDispatchID) New() string {
	s.next++
	return fmt.Sprintf("id-%d", s.next)
}

type fakeDriverInstructionNotifier struct{}

func (f *fakeDriverInstructionNotifier) NotifyJobInstruction(
	_ context.Context,
	_, _, _, _ string,
) error {
	return nil
}

func buildDispatchTestHandler() *Handler {
	clock := fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	ids := &sequenceDispatchID{}
	publishUseCase := application.NewPublishJobStatusChangedUseCase(
		&fakeJobStatusPublisher{},
		clock,
		ids,
		"default",
	)
	lifecycleUseCase := application.NewJobLifecycleUseCase(
		infrastructure.NewInMemoryJobRepository(),
		publishUseCase,
		&fakeDriverInstructionNotifier{},
		clock,
		ids,
		"default",
	)

	return NewHandler(publishUseCase, lifecycleUseCase, trackinggrpc.NewStubTrackingClient())
}

func dispatchRequest(
	t *testing.T,
	mux *http.ServeMux,
	method, path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Buffer
	if body == "" {
		reader = bytes.NewBuffer(nil)
	} else {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	return res
}

func TestHandler_UpdateStatusContract(t *testing.T) {
	useCase := application.NewPublishJobStatusChangedUseCase(
		&fakeJobStatusPublisher{},
		fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		fixedDispatchID{id: "evt-1"},
		"default",
	)
	handler := NewHandler(
		useCase,
		application.NewJobLifecycleUseCase(
			infrastructure.NewInMemoryJobRepository(),
			useCase,
			&fakeDriverInstructionNotifier{},
			fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
			fixedDispatchID{id: "evt-1"},
			"default",
		),
		trackinggrpc.NewStubTrackingClient(),
	)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	res := dispatchRequest(t, mux, http.MethodPost, "/dispatch/jobs/status", `{"job_id":"job-1","new_status":"assigned"}`)

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
	handler := buildDispatchTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/dispatch/eta?job_id=job-1&vehicle_id=vehicle-1&destination=dest", nil)
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
}

func TestHandler_JobLifecycleContract(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"job_id":"job-100",
			"pickup":{"address":"Apapa Port"},
			"dropoff":{"address":"Lekki Free Zone"},
			"priority":"normal"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, createRes.Code)
	}

	schedulingRes := dispatchRequest(
		t,
		mux,
		http.MethodPatch,
		"/dispatch/jobs/job-100/scheduling",
		`{
			"priority":"high",
			"time_window":{
				"pickup_start":"2026-01-01T11:00:00Z",
				"pickup_end":"2026-01-01T12:00:00Z",
				"dropoff_start":"2026-01-01T14:00:00Z",
				"dropoff_end":"2026-01-01T15:00:00Z"
			}
		}`,
	)
	if schedulingRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, schedulingRes.Code)
	}

	assignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/job-100/assign",
		`{"driver_id":"driver-100","vehicle_id":"vehicle-100","reason":"initial assignment"}`,
	)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, assignRes.Code)
	}

	reassignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/job-100/assign",
		`{"driver_id":"driver-101","vehicle_id":"vehicle-101","reason":"breakdown fallback"}`,
	)
	if reassignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, reassignRes.Code)
	}

	ackRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/job-100/acknowledge",
		`{"driver_id":"driver-101"}`,
	)
	if ackRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, ackRes.Code)
	}

	for _, status := range []string{"en-route", "arrived", "loaded", "delivered"} {
		updateRes := dispatchRequest(
			t,
			mux,
			http.MethodPost,
			"/dispatch/jobs/job-100/status",
			fmt.Sprintf(`{"status":"%s"}`, status),
		)
		if updateRes.Code != http.StatusOK {
			t.Fatalf("status %s expected %d, got %d", status, http.StatusOK, updateRes.Code)
		}
	}

	podRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/job-100/pod-attachments",
		`{"kind":"photo","url":"https://example.com/pod.jpg","uploaded_by":"driver-101"}`,
	)
	if podRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, podRes.Code)
	}

	noteRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/job-100/notes",
		`{"text":"Customer requested gate entry call","author_id":"dispatcher-1"}`,
	)
	if noteRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, noteRes.Code)
	}

	timelineRes := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs/job-100/timeline", "")
	if timelineRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, timelineRes.Code)
	}

	var timelinePayload map[string]any
	if err := json.Unmarshal(timelineRes.Body.Bytes(), &timelinePayload); err != nil {
		t.Fatalf("failed to decode timeline response: %v", err)
	}
	timeline, ok := timelinePayload["timeline"].([]any)
	if !ok || len(timeline) == 0 {
		t.Fatalf("expected timeline entries to be present")
	}
}

func TestHandler_AddPODBeforeDeliveryRejected(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{"job_id":"job-200","pickup":{"address":"Ikeja"},"dropoff":{"address":"Yaba"}}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, createRes.Code)
	}

	podRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/job-200/pod-attachments",
		`{"kind":"signature","url":"https://example.com/signature.png"}`,
	)
	if podRes.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, podRes.Code)
	}
}

func TestHandler_DocsContract(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	for _, path := range []string{"/dispatch/docs", "/docs/dispatch"} {
		res := dispatchRequest(t, mux, http.MethodGet, path, "")
		if res.Code != http.StatusOK {
			t.Fatalf("docs path %s expected %d, got %d", path, http.StatusOK, res.Code)
		}
		if !strings.Contains(res.Body.String(), "SwaggerUIBundle") {
			t.Fatalf("docs path %s did not return swagger ui html", path)
		}
	}

	for _, path := range []string{"/dispatch/docs/openapi.json", "/docs/dispatch/openapi.json"} {
		res := dispatchRequest(t, mux, http.MethodGet, path, "")
		if res.Code != http.StatusOK {
			t.Fatalf("openapi path %s expected %d, got %d", path, http.StatusOK, res.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
			t.Fatalf("openapi path %s invalid json: %v", path, err)
		}
		if payload["openapi"] != "3.0.3" {
			t.Fatalf("openapi path %s expected openapi=3.0.3", path)
		}
	}
}
