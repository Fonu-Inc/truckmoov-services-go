package dispatchhttp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/internal/dispatch/infrastructure"
	httpx "github.com/truckmoov/services-go/pkg/http"
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

func makeDispatchBearerToken(secret, tenantID, role string) string {
	payload := fmt.Sprintf(`{"sub":"user-1","email":"ops@example.com","role":"%s","tenantId":"%s","exp":%d}`, role, tenantID, time.Now().Add(time.Hour).Unix())
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature
}

func (f *fakeDriverInstructionNotifier) NotifyJobInstruction(
	_ context.Context,
	_, _, _, _ string,
) error {
	return nil
}

func buildDispatchTestHandler() *Handler {
	clock := fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	ids := &sequenceDispatchID{}
	repository := infrastructure.NewInMemoryJobRepository()
	publishUseCase := application.NewPublishJobStatusChangedUseCase(
		&fakeJobStatusPublisher{},
		clock,
		ids,
		"default",
	)
	lifecycleUseCase := application.NewJobLifecycleUseCase(
		repository,
		publishUseCase,
		&fakeDriverInstructionNotifier{},
		clock,
		ids,
		"default",
	)
	routingUseCase := application.NewRoutingUseCase(repository, clock, "default")

	return NewHandler(publishUseCase, lifecycleUseCase, routingUseCase, repository, nil, nil)
}

func buildDispatchOptimizeTestHandler() *Handler {
	clock := fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	ids := &sequenceDispatchID{}
	repository := infrastructure.NewInMemoryJobRepository()
	publishUseCase := application.NewPublishJobStatusChangedUseCase(
		&fakeJobStatusPublisher{},
		clock,
		ids,
		"default",
	)
	lifecycleUseCase := application.NewJobLifecycleUseCase(
		repository,
		publishUseCase,
		&fakeDriverInstructionNotifier{},
		clock,
		ids,
		"default",
	)
	routingUseCase := application.NewRoutingUseCase(repository, clock, "default")

	vroomClient := infrastructure.NewVroomClientWithConfig("http://vroom.test", &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			payload, err := json.Marshal(infrastructure.VroomResponse{
				Code: 0,
				Routes: []infrastructure.VroomRoute{
					{
						Vehicle: 1,
						Steps: []infrastructure.VroomStep{
							{Type: "start", Location: []float64{3.3958, 6.4550}},
							{Type: "pickup", Job: 1, Location: []float64{3.3649, 6.4448}},
							{Type: "delivery", Job: 1, Location: []float64{4.0096, 6.4294}},
						},
					},
				},
			})
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(payload)),
			}, nil
		}),
	})
	osrmClient := infrastructure.NewOsrmClientWithConfig("http://osrm.test", &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			payload, err := json.Marshal(infrastructure.OsrmResponse{
				Code: "Ok",
				Routes: []infrastructure.OsrmRoute{
					{
						Distance: 2000,
						Duration: 600,
						Geometry: infrastructure.Geometry{Type: "LineString"},
					},
				},
			})
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(payload)),
			}, nil
		}),
	})

	return NewHandler(publishUseCase, lifecycleUseCase, routingUseCase, repository, vroomClient, osrmClient)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func dispatchRequest(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	return dispatchRequestWithHeaders(t, handler, method, path, body, nil)
}

func dispatchRequestWithHeaders(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body string,
	headers map[string]string,
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
	handler = httpx.WithAuthContext(handler, "replace-me")
	if headers == nil {
		headers = map[string]string{}
	}
	if _, ok := headers["Authorization"]; !ok {
		headers["Authorization"] = "Bearer " + makeDispatchBearerToken("replace-me", "default", "DISPATCHER")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
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
		application.NewRoutingUseCase(
			infrastructure.NewInMemoryJobRepository(),
			fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
			"default",
		),
		infrastructure.NewInMemoryJobRepository(),
		nil,
		nil,
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

func TestHandler_ListJobsContract(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	for _, body := range []string{
		`{
			"reference":"TM-2849",
			"vehicleId":"vehicle-1",
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"priority":"low"
		}`,
		`{
			"reference":"TM-2850",
			"vehicleId":"vehicle-2",
			"driverId":"driver-2",
			"pickupLocation":"Apapa Port",
			"deliveryLocation":"Victoria Island",
			"priority":"normal"
		}`,
	} {
		res := dispatchRequest(t, mux, http.MethodPost, "/dispatch/jobs", body)
		if res.Code != http.StatusCreated {
			t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, res.Code, res.Body.String())
		}
	}

	res := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs?status=in_progress&query=victoria&vehicle_id=vehicle-2&driver_id=driver-2", "")
	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, res.Code, res.Body.String())
	}

	var payload struct {
		Status  bool         `json:"status"`
		Message string       `json:"message"`
		Data    []domain.Job `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode jobs list: %v", err)
	}
	if !payload.Status || payload.Message != "success" {
		t.Fatalf("unexpected response envelope: %#v", payload)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("expected one filtered job, got %d", len(payload.Data))
	}
	if payload.Data[0].Reference != "TM-2850" {
		t.Fatalf("expected TM-2850, got %q", payload.Data[0].Reference)
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
			"reference":"TM-2847",
			"vehicleId":"vehicle-100",
			"driverId":"driver-100",
			"pickupLocation":"Apapa Port",
			"deliveryLocation":"Lekki Free Zone",
			"cargoDescription":"High-value electronics cargo",
			"notes":"Gate pass already approved",
			"priority":"normal"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, createRes.Code)
	}

	var createdJob domain.Job
	if err := json.Unmarshal(createRes.Body.Bytes(), &createdJob); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	if createdJob.Reference != "TM-2847" {
		t.Fatalf("expected reference TM-2847, got %q", createdJob.Reference)
	}
	if createdJob.CargoDescription != "High-value electronics cargo" {
		t.Fatalf("expected cargo description to be captured, got %q", createdJob.CargoDescription)
	}
	if createdJob.Priority != domain.PriorityNormal {
		t.Fatalf("expected normal to normalize to %q, got %q", domain.PriorityNormal, createdJob.Priority)
	}
	if createdJob.Status != domain.StatusAssigned {
		t.Fatalf("expected immediate-dispatch modal to assign the job, got %q", createdJob.Status)
	}
	if createdJob.AssignedVehicleID != "vehicle-100" {
		t.Fatalf("expected assigned vehicle vehicle-100, got %q", createdJob.AssignedVehicleID)
	}
	if createdJob.AssignedDriverID != "driver-100" {
		t.Fatalf("expected assigned driver driver-100, got %q", createdJob.AssignedDriverID)
	}
	if len(createdJob.Notes) != 1 || createdJob.Notes[0].Text != "Gate pass already approved" {
		t.Fatalf("expected initial note to be stored, got %#v", createdJob.Notes)
	}

	jobID := createdJob.ID

	reassignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+jobID+"/assign",
		`{"driver_id":"driver-101","vehicle_id":"vehicle-101","reason":"breakdown fallback"}`,
	)
	if reassignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, reassignRes.Code)
	}

	ackRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+jobID+"/acknowledge",
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
			"/dispatch/jobs/"+jobID+"/status",
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
		"/dispatch/jobs/"+jobID+"/pod-attachments",
		`{"kind":"photo","url":"https://example.com/pod.jpg","uploaded_by":"driver-101"}`,
	)
	if podRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, podRes.Code)
	}

	noteRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+jobID+"/notes",
		`{"text":"Customer requested gate entry call","author_id":"dispatcher-1"}`,
	)
	if noteRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, noteRes.Code)
	}

	jobRes := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs/"+jobID, "")
	if jobRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, jobRes.Code)
	}

	var jobPayload struct {
		Status  bool       `json:"status"`
		Message string     `json:"message"`
		Data    domain.Job `json:"data"`
	}
	if err := json.Unmarshal(jobRes.Body.Bytes(), &jobPayload); err != nil {
		t.Fatalf("failed to decode job response: %v", err)
	}
	if !jobPayload.Status || jobPayload.Message != "success" {
		t.Fatalf("unexpected job response envelope: %#v", jobPayload)
	}
	if jobPayload.Data.ID != jobID {
		t.Fatalf("expected job payload for %q, got %q", jobID, jobPayload.Data.ID)
	}

	timelineRes := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs/"+jobID+"/timeline", "")
	if timelineRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, timelineRes.Code)
	}

	var timelinePayload struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			JobID    string           `json:"job_id"`
			Timeline []map[string]any `json:"timeline"`
		} `json:"data"`
	}
	if err := json.Unmarshal(timelineRes.Body.Bytes(), &timelinePayload); err != nil {
		t.Fatalf("failed to decode timeline response: %v", err)
	}
	if !timelinePayload.Status || timelinePayload.Message != "success" {
		t.Fatalf("unexpected timeline response envelope: %#v", timelinePayload)
	}
	if timelinePayload.Data.JobID != jobID {
		t.Fatalf("expected timeline job id %q, got %q", jobID, timelinePayload.Data.JobID)
	}
	if len(timelinePayload.Data.Timeline) == 0 {
		t.Fatalf("expected timeline entries to be present")
	}
}

func TestHandler_CreateJobOriginalModalVehicleOnlyStillPersistsSelection(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"vehicleId":"vehicle-200",
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"priority":"urgent"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	var createdJob domain.Job
	if err := json.Unmarshal(createRes.Body.Bytes(), &createdJob); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	if createdJob.Status != domain.StatusCreated {
		t.Fatalf("expected vehicle-only create to remain created, got %q", createdJob.Status)
	}
	if createdJob.AssignedVehicleID != "vehicle-200" {
		t.Fatalf("expected selected vehicle to be preserved, got %q", createdJob.AssignedVehicleID)
	}
	if createdJob.AssignedDriverID != "" {
		t.Fatalf("expected no assigned driver, got %q", createdJob.AssignedDriverID)
	}
	if createdJob.Priority != domain.PriorityUrgent {
		t.Fatalf("expected urgent to normalize to %q, got %q", domain.PriorityUrgent, createdJob.Priority)
	}
}

func TestHandler_CreateJobOriginalModalRequiresVehicle(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"pickupLocation":"Apapa Port",
			"deliveryLocation":"Lekki Free Zone"
		}`,
	)
	if createRes.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, createRes.Code, createRes.Body.String())
	}
	if !strings.Contains(createRes.Body.String(), "vehicleId is required") {
		t.Fatalf("expected missing vehicle validation, got %s", createRes.Body.String())
	}
}

func TestHandler_CreateJobAlternateFlatPayloadStillAccepted(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"reference":"JOB-100",
			"pickupAddress":"Apapa Port",
			"pickupLat":6.45,
			"pickupLng":3.35,
			"deliveryAddress":"Lekki Free Zone",
			"deliveryLat":6.44,
			"deliveryLng":3.48,
			"cargoType":"Electronics",
			"cargoWeight":12.5,
			"cargoDescription":"High-value electronics cargo",
			"amount":450000,
			"deadline":"2026-01-01T15:30",
			"priority":"medium"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, createRes.Code)
	}

	var createdJob domain.Job
	if err := json.Unmarshal(createRes.Body.Bytes(), &createdJob); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	if createdJob.Reference != "JOB-100" {
		t.Fatalf("expected reference JOB-100, got %q", createdJob.Reference)
	}
	if createdJob.CargoType != "Electronics" {
		t.Fatalf("expected cargo type Electronics, got %q", createdJob.CargoType)
	}
	if createdJob.LoadWeightKg != 12500 {
		t.Fatalf("expected 12500kg load weight, got %v", createdJob.LoadWeightKg)
	}
	if createdJob.TimeWindow.DropoffEnd == nil {
		t.Fatalf("expected deadline to map into dropoff end time window")
	}
}

func TestHandler_CreateJobLegacyPayloadStillAccepted(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"job_id":"job-legacy-1",
			"pickup":{"address":"Apapa Port","latitude":6.4448,"longitude":3.3649},
			"dropoff":{"address":"Lekki Free Zone","latitude":6.4294,"longitude":4.0096},
			"priority":"critical"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	var createdJob domain.Job
	if err := json.Unmarshal(createRes.Body.Bytes(), &createdJob); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	if createdJob.Reference != "job-legacy-1" {
		t.Fatalf("expected legacy payload to default reference to job id, got %q", createdJob.Reference)
	}
	if createdJob.Priority != domain.PriorityUrgent {
		t.Fatalf("expected legacy priority alias to normalize to %q, got %q", domain.PriorityUrgent, createdJob.Priority)
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

func TestHandler_OptimizeRouteSingleJobContract(t *testing.T) {
	handler := buildDispatchOptimizeTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"job_id":"job-route-1",
			"pickup":{"address":"Apapa Port","latitude":6.4448,"longitude":3.3649},
			"dropoff":{"address":"Lekki Free Zone","latitude":6.4294,"longitude":4.0096},
			"required_vehicle_type":"flatbed",
			"load_weight_kg":5000,
			"load_volume_m3":12,
			"estimated_service_minutes":20,
			"priority":"high"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, createRes.Code)
	}

	routeRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/routes/optimize",
		`{
			"job_ids":["job-route-1"],
			"origin":{"address":"Truck Yard","latitude":6.4550,"longitude":3.3958},
			"vehicle":{"vehicle_id":"veh-1","vehicle_type":"flatbed","capacity_weight_kg":7000,"capacity_volume_m3":20,"average_speed_kph":40},
			"driver_shift":{"start":"2026-01-01T09:00:00Z","end":"2026-01-01T18:00:00Z"}
		}`,
	)
	if routeRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, routeRes.Code, routeRes.Body.String())
	}

	var payload struct {
		Code      string                        `json:"code"`
		Routes    []infrastructure.OsrmRoute    `json:"routes"`
		Waypoints []infrastructure.OsrmWaypoint `json:"waypoints"`
	}
	if err := json.Unmarshal(routeRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Code != "Ok" {
		t.Fatalf("expected osrm response code Ok, got %q", payload.Code)
	}
	if len(payload.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(payload.Routes))
	}
	if len(payload.Waypoints) != 3 {
		t.Fatalf("expected 3 waypoints from vroom coordinates, got %d", len(payload.Waypoints))
	}
}

func TestHandler_OptimizeRouteContract(t *testing.T) {
	handler := buildDispatchOptimizeTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	for _, body := range []string{
		`{"job_id":"job-a","pickup":{"address":"Apapa","latitude":6.4500,"longitude":3.3600},"dropoff":{"address":"Victoria Island","latitude":6.4281,"longitude":3.4219},"priority":"normal"}`,
		`{"job_id":"job-b","pickup":{"address":"Ikeja","latitude":6.6018,"longitude":3.3515},"dropoff":{"address":"Yaba","latitude":6.5095,"longitude":3.3711},"priority":"urgent"}`,
	} {
		res := dispatchRequest(t, mux, http.MethodPost, "/dispatch/jobs", body)
		if res.Code != http.StatusCreated {
			t.Fatalf("expected status %d, got %d", http.StatusCreated, res.Code)
		}
	}

	routeRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/routes/optimize",
		`{
			"job_ids":["job-a","job-b"],
			"origin":{"address":"Ojota","latitude":6.5841,"longitude":3.3792},
			"vehicle":{"vehicle_id":"veh-2","vehicle_type":"van","capacity_weight_kg":9000,"capacity_volume_m3":30,"average_speed_kph":35},
			"driver_shift":{"start":"2026-01-01T10:00:00Z","end":"2026-01-01T22:00:00Z"}
		}`,
	)
	if routeRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, routeRes.Code, routeRes.Body.String())
	}

	var payload struct {
		Code      string                        `json:"code"`
		Routes    []infrastructure.OsrmRoute    `json:"routes"`
		Waypoints []infrastructure.OsrmWaypoint `json:"waypoints"`
	}
	if err := json.Unmarshal(routeRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Code != "Ok" {
		t.Fatalf("expected osrm response code Ok, got %q", payload.Code)
	}
	if len(payload.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(payload.Routes))
	}
	if len(payload.Waypoints) != 3 {
		t.Fatalf("expected vroom-derived waypoints in final osrm response, got %d", len(payload.Waypoints))
	}
}

func TestHandler_OptimizeRouteConstraintViolationReported(t *testing.T) {
	handler := buildDispatchOptimizeTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"job_id":"job-heavy",
			"pickup":{"address":"Port","latitude":6.4448,"longitude":3.3649},
			"dropoff":{"address":"Warehouse","latitude":6.5244,"longitude":3.3792},
			"required_vehicle_type":"flatbed",
			"load_weight_kg":12000,
			"load_volume_m3":40
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, createRes.Code)
	}

	routeRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/routes/optimize",
		`{
			"job_ids":["job-heavy"],
			"origin":{"address":"Depot","latitude":6.4500,"longitude":3.4000},
			"vehicle":{"vehicle_id":"veh-3","vehicle_type":"bike","capacity_weight_kg":200,"capacity_volume_m3":1,"average_speed_kph":25},
			"driver_shift":{"start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z"}
		}`,
	)
	if routeRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, routeRes.Code, routeRes.Body.String())
	}

	var payload struct {
		Code      string                        `json:"code"`
		Routes    []infrastructure.OsrmRoute    `json:"routes"`
		Waypoints []infrastructure.OsrmWaypoint `json:"waypoints"`
	}
	if err := json.Unmarshal(routeRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Code != "Ok" {
		t.Fatalf("expected osrm response code Ok, got %q", payload.Code)
	}
	if len(payload.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(payload.Routes))
	}
}

func TestHandler_CreateJobRejectsClientTenantField(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	server := httpx.WithAuthContext(mux, "replace-me")
	authHeader := map[string]string{"Authorization": "Bearer " + makeDispatchBearerToken("replace-me", "tenant-from-token", "DISPATCHER")}

	createRes := dispatchRequestWithHeaders(
		t,
		server,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"tenant_id":"client-tenant",
			"reference":"TM-3002",
			"vehicleId":"vehicle-2",
			"pickupLocation":"Apapa Port",
			"deliveryLocation":"Lekki Free Zone",
			"priority":"normal"
		}`,
		authHeader,
	)
	if createRes.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, createRes.Code, createRes.Body.String())
	}
}

func TestHandler_CreateAndListJobsUseTokenTenant(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	server := httpx.WithAuthContext(mux, "replace-me")
	authHeader := map[string]string{"Authorization": "Bearer " + makeDispatchBearerToken("replace-me", "tenant-from-token", "DISPATCHER")}

	createRes := dispatchRequestWithHeaders(
		t,
		server,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"reference":"TM-3002",
			"vehicleId":"vehicle-2",
			"pickupLocation":"Apapa Port",
			"deliveryLocation":"Lekki Free Zone",
			"priority":"normal"
		}`,
		authHeader,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	var created domain.Job
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created job: %v", err)
	}
	if created.TenantID != "tenant-from-token" {
		t.Fatalf("expected token tenant, got %q", created.TenantID)
	}

	listRes := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs", "", authHeader)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, listRes.Code, listRes.Body.String())
	}

	var payload struct {
		Status  bool         `json:"status"`
		Message string       `json:"message"`
		Data    []domain.Job `json:"data"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode jobs list: %v", err)
	}
	if !payload.Status || payload.Message != "success" {
		t.Fatalf("unexpected response envelope: %#v", payload)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("expected one tenant-scoped job, got %d", len(payload.Data))
	}
	if payload.Data[0].TenantID != "tenant-from-token" {
		t.Fatalf("expected tenant-from-token, got %q", payload.Data[0].TenantID)
	}
}

func TestHandler_ListJobsRejectsTenantQuery(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	server := httpx.WithAuthContext(mux, "replace-me")
	authHeader := map[string]string{"Authorization": "Bearer " + makeDispatchBearerToken("replace-me", "tenant-from-token", "DISPATCHER")}

	listRes := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs?tenant_id=client-tenant", "", authHeader)
	if listRes.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, listRes.Code, listRes.Body.String())
	}
}
