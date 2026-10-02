package dispatchhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/internal/dispatch/infrastructure"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type stubFleetDirectory struct {
	known map[string]bool
	err   error
}

func (s stubFleetDirectory) DriverExists(_ context.Context, _, driverID string) (bool, error) {
	return s.known[driverID], s.err
}

func (s stubFleetDirectory) VehicleExists(_ context.Context, _, vehicleID string) (bool, error) {
	return s.known[vehicleID], s.err
}

type countingInstructionNotifier struct{ calls int }

func (n *countingInstructionNotifier) NotifyJobInstruction(_ context.Context, _ domain.Job, _ string) error {
	n.calls++
	return nil
}

type assignHarness struct {
	mux      *http.ServeMux
	jobs     *infrastructure.InMemoryJobRepository
	notifier *countingInstructionNotifier
	jobID    string
}

func newAssignHarness(t *testing.T, fleet application.FleetDirectory) assignHarness {
	t.Helper()
	clock := fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	ids := &sequenceDispatchID{}
	h := assignHarness{mux: http.NewServeMux(), jobs: infrastructure.NewInMemoryJobRepository(), notifier: &countingInstructionNotifier{}}
	publish := application.NewPublishJobStatusChangedUseCase(&fakeJobStatusPublisher{}, clock, ids, "default")
	lifecycle := application.NewJobLifecycleUseCase(h.jobs, publish, h.notifier, clock, ids, "default").WithFleetDirectory(fleet)
	NewHandler(publish, lifecycle, nil, nil, nil, nil).RegisterRoutes(httpx.NewRoutes(h.mux, false, nil, nil))

	res := dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs", `{"pickup":{"address":"Apapa"},"dropoff":{"address":"Lekki"}}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("create job: expected %d, got %d body=%s", http.StatusCreated, res.Code, res.Body.String())
	}
	h.jobID = decodeJobBody(t, res.Body.Bytes()).ID
	return h
}

func (h assignHarness) assign(t *testing.T, body string) (int, string, domain.Job) {
	t.Helper()
	res := dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs/"+h.jobID+"/assign", body)
	var envelope struct {
		Message string     `json:"message"`
		Data    domain.Job `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode assign response: %v body=%s", err, res.Body.String())
	}
	return res.Code, envelope.Message, envelope.Data
}

func (h assignHarness) stored(t *testing.T) domain.Job {
	t.Helper()
	job, ok := h.jobs.FindByTenantAndID("default", h.jobID)
	if !ok {
		t.Fatal("job missing")
	}
	return job
}

func TestAssignRefusesAnUnknownDriverOrVehicle(t *testing.T) {
	cases := []struct{ body, message string }{
		{`{"driver_id":"00000000-0000-0000-0000-000000000000"}`, "driver not found"},
		{`{"vehicle_id":"not-a-uuid"}`, "vehicle not found"},
	}
	for _, tc := range cases {
		h := newAssignHarness(t, stubFleetDirectory{known: map[string]bool{"driver-1": true}})

		code, message, _ := h.assign(t, tc.body)
		if code != http.StatusNotFound || message != tc.message {
			t.Fatalf("%s: expected 404 %q, got %d %q", tc.body, tc.message, code, message)
		}
		if stored := h.stored(t); stored.AssignedDriverID != "" || stored.AssignedVehicleID != "" || h.notifier.calls != 0 {
			t.Fatalf("%s: expected nothing saved or sent, got driver=%q vehicle=%q notified=%d", tc.body, stored.AssignedDriverID, stored.AssignedVehicleID, h.notifier.calls)
		}
	}
}

func TestAssignAnswers503WhenFleetIsUnreachable(t *testing.T) {
	h := newAssignHarness(t, stubFleetDirectory{err: errors.New("dial tcp 10.0.0.1: connection refused")})

	code, message, _ := h.assign(t, `{"driver_id":"driver-1"}`)
	if code != http.StatusServiceUnavailable || message != application.ErrFleetUnavailable.Error() {
		t.Fatalf("expected 503 %q, got %d %q", application.ErrFleetUnavailable.Error(), code, message)
	}
	if h.stored(t).AssignedDriverID != "" || h.notifier.calls != 0 {
		t.Fatal("expected nothing assigned or sent while fleet is unreachable")
	}
}

func TestAssignVehicleThenDriverThenSwapVehicle(t *testing.T) {
	h := newAssignHarness(t, stubFleetDirectory{known: map[string]bool{"driver-1": true, "vehicle-1": true, "vehicle-2": true}})

	_, _, job := h.assign(t, `{"vehicle_id":"vehicle-1"}`)
	if last := job.Timeline[len(job.Timeline)-1]; last.Type != "vehicle_assigned" || job.Status != domain.StatusCreated {
		t.Fatalf("vehicle alone: expected vehicle_assigned and created, got %s %s", last.Type, job.Status)
	}

	_, _, job = h.assign(t, `{"driver_id":"driver-1"}`)
	if !hasTimelineType(job, "job_assigned") || hasTimelineType(job, "job_reassigned") || h.notifier.calls != 1 {
		t.Fatalf("first driver: expected job_assigned and one instruction, got %v notified=%d", timelineTypes(job), h.notifier.calls)
	}

	code, _, job := h.assign(t, `{"vehicle_id":"vehicle-2"}`)
	if code != http.StatusOK || job.AssignedVehicleID != "vehicle-2" || h.notifier.calls != 1 {
		t.Fatalf("vehicle swap: expected vehicle-2 and no new instruction, got %d %q notified=%d", code, job.AssignedVehicleID, h.notifier.calls)
	}
}

func hasTimelineType(job domain.Job, eventType string) bool {
	for _, entry := range job.Timeline {
		if entry.Type == eventType {
			return true
		}
	}
	return false
}

func timelineTypes(job domain.Job) []string {
	types := make([]string, 0, len(job.Timeline))
	for _, entry := range job.Timeline {
		types = append(types, entry.Type)
	}
	return types
}
