package dispatchhttp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

func decodeJobBody(t *testing.T, body []byte) domain.Job {
	t.Helper()
	var env struct {
		Data domain.Job `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode job envelope: %v", err)
	}
	return env.Data
}

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

type fakeJobInstructionSender struct {
	payloads []application.DispatchJobInstructionPayload
	err      error
}

func (f *fakeJobInstructionSender) SendJobInstruction(_ context.Context, payload application.DispatchJobInstructionPayload) error {
	f.payloads = append(f.payloads, payload)
	return f.err
}

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

func buildDispatchTestHandlerWithSender() (*Handler, *fakeJobInstructionSender) {
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
	sender := &fakeJobInstructionSender{}
	sendJobInstructionUseCase := application.NewSendJobInstructionUseCase(repository, sender, clock, ids, "default")

	return NewHandler(publishUseCase, lifecycleUseCase, routingUseCase, sendJobInstructionUseCase, nil, nil), sender
}

func buildDispatchTestHandler() *Handler {
	handler, _ := buildDispatchTestHandlerWithSender()
	return handler
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
	handler = httpx.WithAuthContext(handler, identity.SharedSecretVerifier("replace-me"))
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
	clock := fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	idGenerator := fixedDispatchID{id: "evt-1"}
	repository := infrastructure.NewInMemoryJobRepository()
	useCase := application.NewPublishJobStatusChangedUseCase(
		&fakeJobStatusPublisher{},
		clock,
		idGenerator,
		"default",
	)
	handler := NewHandler(
		useCase,
		application.NewJobLifecycleUseCase(
			repository,
			useCase,
			&fakeDriverInstructionNotifier{},
			clock,
			idGenerator,
			"default",
		),
		application.NewRoutingUseCase(repository, clock, "default"),
		application.NewSendJobInstructionUseCase(repository, &fakeJobInstructionSender{}, clock, idGenerator, "default"),
		nil,
		nil,
	)

	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

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
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	for _, body := range []string{
		`{
			"reference":"TM-2849",
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"priority":"low"
		}`,
		`{
			"reference":"TM-2850",
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

	listRes := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs?query=TM-2850", "")
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, listRes.Code, listRes.Body.String())
	}
	var listPayload struct {
		Data []domain.Job `json:"data"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode jobs list: %v", err)
	}
	if len(listPayload.Data) != 1 {
		t.Fatalf("expected one TM-2850 job, got %d", len(listPayload.Data))
	}
	assignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+listPayload.Data[0].ID+"/assign",
		`{"vehicle_id":"vehicle-2","driver_id":"driver-2"}`,
	)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, assignRes.Code, assignRes.Body.String())
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
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"reference":"TM-2847",
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

	createdJob := decodeJobBody(t, createRes.Body.Bytes())
	if createdJob.Reference != "TM-2847" {
		t.Fatalf("expected reference TM-2847, got %q", createdJob.Reference)
	}
	if createdJob.CargoDescription != "High-value electronics cargo" {
		t.Fatalf("expected cargo description to be captured, got %q", createdJob.CargoDescription)
	}
	if createdJob.Priority != domain.PriorityNormal {
		t.Fatalf("expected normal to normalize to %q, got %q", domain.PriorityNormal, createdJob.Priority)
	}
	if createdJob.Status != domain.StatusCreated {
		t.Fatalf("expected job creation to never assign, got %q", createdJob.Status)
	}
	if len(createdJob.Notes) != 1 || createdJob.Notes[0].Text != "Gate pass already approved" {
		t.Fatalf("expected initial note to be stored, got %#v", createdJob.Notes)
	}

	jobID := createdJob.ID

	initialAssignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+jobID+"/assign",
		`{"vehicle_id":"vehicle-100","driver_id":"driver-100","driver_phone":"+2348010000100"}`,
	)
	if initialAssignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, initialAssignRes.Code)
	}
	assignedJob := decodeJobBody(t, initialAssignRes.Body.Bytes())
	if assignedJob.AssignedVehicleID != "vehicle-100" || assignedJob.AssignedDriverID != "driver-100" || assignedJob.AssignedDriverPhone != "+2348010000100" {
		t.Fatalf("expected assigned vehicle/driver/phone to be stored, got %#v", assignedJob)
	}

	reassignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+jobID+"/assign",
		`{"driver_id":"driver-101","driver_phone":"+2348010000101","vehicle_id":"vehicle-101","reason":"breakdown fallback"}`,
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

func TestHandler_SendWhatsAppInstructionContract(t *testing.T) {
	handler, sender := buildDispatchTestHandlerWithSender()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"reference":"TM-2849",
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"cargoDescription":"Food products",
			"priority":"normal"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	createdJob := decodeJobBody(t, createRes.Body.Bytes())

	assignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/assign",
		`{"vehicle_id":"vehicle-1","driver_id":"driver-100","driver_phone":"+2348012345678"}`,
	)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, assignRes.Code, assignRes.Body.String())
	}

	sendRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/send-whatsapp-instruction",
		`{
			"driverId":"driver-100",
			"templateName":"job_assignment_v1",
			"driverName":"Chukwuma Okafor"
		}`,
	)
	if sendRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, sendRes.Code, sendRes.Body.String())
	}

	if len(sender.payloads) != 1 {
		t.Fatalf("expected one send payload, got %d", len(sender.payloads))
	}
	if sender.payloads[0].JobReference != "TM-2849" {
		t.Fatalf("expected reference TM-2849, got %q", sender.payloads[0].JobReference)
	}
	if sender.payloads[0].PickupLocation != "Oshodi Market" {
		t.Fatalf("expected pickup location Oshodi Market, got %q", sender.payloads[0].PickupLocation)
	}
	if sender.payloads[0].DeliveryLocation != "Ikorodu" {
		t.Fatalf("expected delivery location Ikorodu, got %q", sender.payloads[0].DeliveryLocation)
	}
	if sender.payloads[0].CargoDescription != "Food products" {
		t.Fatalf("expected cargo description Food products, got %q", sender.payloads[0].CargoDescription)
	}
	if sender.payloads[0].Recipient != "+2348012345678" {
		t.Fatalf("expected stored assigned driver phone to be used, got %q", sender.payloads[0].Recipient)
	}

	var payload struct {
		Status  bool       `json:"status"`
		Message string     `json:"message"`
		Data    domain.Job `json:"data"`
	}
	if err := json.Unmarshal(sendRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode send response: %v", err)
	}
	if !payload.Status || payload.Message != "success" {
		t.Fatalf("unexpected response envelope: %#v", payload)
	}
	found := false
	for _, entry := range payload.Data.Timeline {
		if entry.Type == "driver_instruction_sent" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected driver_instruction_sent timeline entry")
	}
}

func TestHandler_SendJobInstructionRejectsMismatchedDriver(t *testing.T) {
	handler, _ := buildDispatchTestHandlerWithSender()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"cargoDescription":"Food products",
			"priority":"normal"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	createdJob := decodeJobBody(t, createRes.Body.Bytes())

	assignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/assign",
		`{"vehicle_id":"vehicle-100","driver_id":"driver-100","driver_phone":"+2348012345678"}`,
	)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, assignRes.Code, assignRes.Body.String())
	}

	sendRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/send-whatsapp-instruction",
		`{
			"driverId":"driver-999",
			"to":"+2348012345678",
			"templateName":"job_assignment_v1",
			"driverName":"Chukwuma Okafor"
		}`,
	)
	if sendRes.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, sendRes.Code, sendRes.Body.String())
	}
}

func TestHandler_SendJobInstructionRejectsJobPastAssigned(t *testing.T) {
	handler, sender := buildDispatchTestHandlerWithSender()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"cargoDescription":"Food products",
			"priority":"normal"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	createdJob := decodeJobBody(t, createRes.Body.Bytes())

	assignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/assign",
		`{"vehicle_id":"vehicle-100","driver_id":"driver-100","driver_phone":"+2348012345678"}`,
	)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, assignRes.Code, assignRes.Body.String())
	}

	statusRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/status",
		`{"status":"en-route"}`,
	)
	if statusRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, statusRes.Code, statusRes.Body.String())
	}

	sendRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/send-whatsapp-instruction",
		`{
			"driverId":"driver-100",
			"to":"+2348012345678",
			"templateName":"job_assignment_v1",
			"driverName":"Chukwuma Okafor"
		}`,
	)
	if sendRes.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, sendRes.Code, sendRes.Body.String())
	}
	if len(sender.payloads) != 0 {
		t.Fatalf("expected no job instruction send after en-route, got %d payloads", len(sender.payloads))
	}
}

func TestHandler_SendJobInstructionRejectsMismatchedStoredPhone(t *testing.T) {
	handler, _ := buildDispatchTestHandlerWithSender()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"cargoDescription":"Food products",
			"priority":"normal"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	createdJob := decodeJobBody(t, createRes.Body.Bytes())

	assignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/assign",
		`{"vehicle_id":"vehicle-100","driver_id":"driver-100","driver_phone":"+2348012345678"}`,
	)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, assignRes.Code, assignRes.Body.String())
	}

	sendRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/send-whatsapp-instruction",
		`{
			"driverId":"driver-100",
			"to":"+2348099999999",
			"templateName":"job_assignment_v1",
			"driverName":"Chukwuma Okafor"
		}`,
	)
	if sendRes.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, sendRes.Code, sendRes.Body.String())
	}
}

func TestHandler_DriverStatusRequestWorkflow(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"cargoDescription":"Food products",
			"priority":"normal"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	createdJob := decodeJobBody(t, createRes.Body.Bytes())

	assignRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/assign",
		`{"vehicle_id":"vehicle-100","driver_id":"driver-100","driver_phone":"+2348012345678"}`,
	)
	if assignRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, assignRes.Code, assignRes.Body.String())
	}

	requestRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/driver-status-requests",
		`{
			"driver_id":"driver-100",
			"driver_phone":"+2348012345678",
			"keyword":"ENROUTE",
			"raw_message":"ENROUTE",
			"source_message_id":"wamid.inbound.1",
			"channel":"whatsapp"
		}`,
	)
	if requestRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, requestRes.Code, requestRes.Body.String())
	}

	var createPayload struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Job     domain.Job                 `json:"job"`
			Request domain.DriverStatusRequest `json:"request"`
		} `json:"data"`
	}
	if err := json.Unmarshal(requestRes.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("decode create request response: %v", err)
	}
	if createPayload.Data.Job.Status != domain.StatusAssigned {
		t.Fatalf("expected job to remain assigned before confirmation, got %q", createPayload.Data.Job.Status)
	}
	if createPayload.Data.Request.RequestedStatus != domain.StatusEnRoute {
		t.Fatalf("expected requested status en-route, got %q", createPayload.Data.Request.RequestedStatus)
	}

	confirmRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/driver-status-requests/"+createPayload.Data.Request.ID+"/confirm",
		"",
	)
	if confirmRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, confirmRes.Code, confirmRes.Body.String())
	}

	var confirmPayload struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Job     domain.Job                 `json:"job"`
			Request domain.DriverStatusRequest `json:"request"`
		} `json:"data"`
	}
	if err := json.Unmarshal(confirmRes.Body.Bytes(), &confirmPayload); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if confirmPayload.Data.Job.Status != domain.StatusEnRoute {
		t.Fatalf("expected job status en-route after confirmation, got %q", confirmPayload.Data.Job.Status)
	}
	if confirmPayload.Data.Request.State != domain.DriverStatusRequestConfirmed {
		t.Fatalf("expected confirmed request state, got %q", confirmPayload.Data.Request.State)
	}
}

func TestHandler_UpdateJobStatusRequiresDispatcherRole(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"pickupLocation":"Oshodi Market",
			"deliveryLocation":"Ikorodu",
			"cargoDescription":"Food products",
			"priority":"normal"
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	createdJob := decodeJobBody(t, createRes.Body.Bytes())

	res := dispatchRequestWithHeaders(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs/"+createdJob.ID+"/status",
		`{"status":"en-route"}`,
		map[string]string{
			"Authorization": "Bearer " + makeDispatchBearerToken("replace-me", "default", "FINANCE_VIEWER"),
		},
	)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, res.Code, res.Body.String())
	}
}

func TestHandler_CreateJobAlternateFlatPayloadStillAccepted(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

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

	createdJob := decodeJobBody(t, createRes.Body.Bytes())
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
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

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

	createdJob := decodeJobBody(t, createRes.Body.Bytes())
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
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

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
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

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

func TestHandler_JobsSummaryCountsByStatus(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	body := `{"pickupLocation":"A","deliveryLocation":"B"}`
	if res := dispatchRequest(t, mux, http.MethodPost, "/dispatch/jobs", body); res.Code != http.StatusCreated {
		t.Fatalf("create job expected 201, got %d body=%s", res.Code, res.Body.String())
	}

	res := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs/summary", "")
	if res.Code != http.StatusOK {
		t.Fatalf("summary expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Data struct {
			Total    int            `json:"total"`
			ByStatus map[string]int `json:"by_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if payload.Data.Total != 1 {
		t.Fatalf("expected total 1, got %d", payload.Data.Total)
	}
	if payload.Data.ByStatus["created"] != 1 {
		t.Fatalf("expected 1 created, got %d (map=%v)", payload.Data.ByStatus["created"], payload.Data.ByStatus)
	}
	if payload.Data.ByStatus["delivered"] != 0 {
		t.Fatalf("expected delivered baseline 0, got %d", payload.Data.ByStatus["delivered"])
	}
}

func TestHandler_OptimizeRouteSingleJobContract(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

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

	var payload application.RoutePlan
	if err := json.Unmarshal(routeRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Feasible {
		t.Fatalf("expected feasible route plan, got %+v", payload)
	}
	if len(payload.OrderedSteps) != 2 {
		t.Fatalf("expected 2 route steps, got %d", len(payload.OrderedSteps))
	}
}

func TestHandler_OptimizeRouteContract(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

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

	var payload application.RoutePlan
	if err := json.Unmarshal(routeRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Feasible {
		t.Fatalf("expected feasible route plan, got %+v", payload)
	}
	if len(payload.OrderedSteps) != 4 {
		t.Fatalf("expected 4 route steps, got %d", len(payload.OrderedSteps))
	}
}

func TestHandler_OptimizeRouteConstraintViolationReported(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

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

	var payload application.RoutePlan
	if err := json.Unmarshal(routeRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Feasible {
		t.Fatalf("expected infeasible route plan, got %+v", payload)
	}
	if len(payload.InfeasibilityReasons) == 0 {
		t.Fatalf("expected infeasibility reasons, got %+v", payload)
	}
}

func TestHandler_CreateJobRejectsClientTenantField(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))
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
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))
	authHeader := map[string]string{"Authorization": "Bearer " + makeDispatchBearerToken("replace-me", "tenant-from-token", "DISPATCHER")}

	createRes := dispatchRequestWithHeaders(
		t,
		server,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"reference":"TM-3002",
			"pickupLocation":"Apapa Port",
			"deliveryLocation":"Lekki Free Zone",
			"priority":"normal"
		}`,
		authHeader,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	created := decodeJobBody(t, createRes.Body.Bytes())
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
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))
	authHeader := map[string]string{"Authorization": "Bearer " + makeDispatchBearerToken("replace-me", "tenant-from-token", "DISPATCHER")}

	listRes := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs?tenant_id=client-tenant", "", authHeader)
	if listRes.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, listRes.Code, listRes.Body.String())
	}
}

func TestHandler_UpdateJobDetailsContract(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{"pickupLocation":"Old Pickup","deliveryLocation":"Old Dropoff"}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}
	jobID := decodeJobBody(t, createRes.Body.Bytes()).ID

	patchRes := dispatchRequest(
		t,
		mux,
		http.MethodPatch,
		"/dispatch/jobs/"+jobID,
		`{
			"pickup":{"address":"New Pickup Address"},
			"dropoff":{"address":"New Dropoff Address"},
			"cargo_type":"Frozen Goods",
			"amount":75000,
			"deadline":"2026-01-02T10:00:00Z",
			"stops":[{"address":"Waypoint 1"}]
		}`,
	)
	if patchRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, patchRes.Code, patchRes.Body.String())
	}

	updatedJob := decodeJobBody(t, patchRes.Body.Bytes())
	if updatedJob.Pickup.Address != "New Pickup Address" {
		t.Fatalf("expected updated pickup address, got %q", updatedJob.Pickup.Address)
	}
	if updatedJob.Dropoff.Address != "New Dropoff Address" {
		t.Fatalf("expected updated dropoff address, got %q", updatedJob.Dropoff.Address)
	}
	if updatedJob.CargoType != "Frozen Goods" {
		t.Fatalf("expected updated cargo type, got %q", updatedJob.CargoType)
	}
	if updatedJob.AmountNgn != 75000 {
		t.Fatalf("expected updated amount 75000, got %v", updatedJob.AmountNgn)
	}
	if updatedJob.Deadline == nil || !updatedJob.Deadline.Equal(time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected updated deadline, got %v", updatedJob.Deadline)
	}
	if len(updatedJob.Stops) != 1 || updatedJob.Stops[0].Address != "Waypoint 1" {
		t.Fatalf("expected updated stops, got %#v", updatedJob.Stops)
	}
}

func TestHandler_UpdateJobDetailsNotFound(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	patchRes := dispatchRequest(
		t,
		mux,
		http.MethodPatch,
		"/dispatch/jobs/does-not-exist",
		`{"pickup":{"address":"A"},"dropoff":{"address":"B"}}`,
	)
	if patchRes.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, patchRes.Code, patchRes.Body.String())
	}
}

func TestHandler_DeleteJobContract(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{"pickupLocation":"A","deliveryLocation":"B"}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}
	jobID := decodeJobBody(t, createRes.Body.Bytes()).ID

	deleteRes := dispatchRequest(t, mux, http.MethodDelete, "/dispatch/jobs/"+jobID, "")
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, deleteRes.Code, deleteRes.Body.String())
	}

	getRes := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs/"+jobID, "")
	if getRes.Code != http.StatusNotFound {
		t.Fatalf("expected deleted job to 404, got %d body=%s", getRes.Code, getRes.Body.String())
	}
}

func TestHandler_DeleteJobNotFound(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	deleteRes := dispatchRequest(t, mux, http.MethodDelete, "/dispatch/jobs/does-not-exist", "")
	if deleteRes.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, deleteRes.Code, deleteRes.Body.String())
	}
}

func TestHandler_JobsSummaryIncludesTotalValueNgn(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	for _, body := range []string{
		`{"pickupLocation":"A","deliveryLocation":"B","amount":100000}`,
		`{"pickupLocation":"C","deliveryLocation":"D","amount":250000}`,
	} {
		res := dispatchRequest(t, mux, http.MethodPost, "/dispatch/jobs", body)
		if res.Code != http.StatusCreated {
			t.Fatalf("create job expected 201, got %d body=%s", res.Code, res.Body.String())
		}
	}

	res := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs/summary", "")
	if res.Code != http.StatusOK {
		t.Fatalf("summary expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Data struct {
			Total         int     `json:"total"`
			TotalValueNgn float64 `json:"total_value_ngn"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if payload.Data.Total != 2 {
		t.Fatalf("expected total 2, got %d", payload.Data.Total)
	}
	if payload.Data.TotalValueNgn != 350000 {
		t.Fatalf("expected total_value_ngn 350000, got %v", payload.Data.TotalValueNgn)
	}
}

func TestHandler_CreateJobWithStopsIncludedInResponse(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{
			"pickupLocation":"A",
			"deliveryLocation":"B",
			"stops":[
				{"address":"Stop 1","contact_name":"John"},
				{"address":"Stop 2"}
			]
		}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}

	createdJob := decodeJobBody(t, createRes.Body.Bytes())
	if len(createdJob.Stops) != 2 {
		t.Fatalf("expected 2 stops, got %#v", createdJob.Stops)
	}
	if createdJob.Stops[0].Address != "Stop 1" || createdJob.Stops[0].ContactName != "John" {
		t.Fatalf("expected stop 1 to match request, got %#v", createdJob.Stops[0])
	}
	if createdJob.Stops[1].Address != "Stop 2" {
		t.Fatalf("expected stop 2 to match request, got %#v", createdJob.Stops[1])
	}
}

func TestHandler_JobResourceBarePathGetAndMethodNotAllowed(t *testing.T) {
	handler := buildDispatchTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	createRes := dispatchRequest(
		t,
		mux,
		http.MethodPost,
		"/dispatch/jobs",
		`{"pickupLocation":"A","deliveryLocation":"B"}`,
	)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, createRes.Code, createRes.Body.String())
	}
	jobID := decodeJobBody(t, createRes.Body.Bytes()).ID

	getRes := dispatchRequest(t, mux, http.MethodGet, "/dispatch/jobs/"+jobID, "")
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, getRes.Code, getRes.Body.String())
	}
	if decodeJobBody(t, getRes.Body.Bytes()).ID != jobID {
		t.Fatalf("expected GET to return job %q", jobID)
	}

	postRes := dispatchRequest(t, mux, http.MethodPost, "/dispatch/jobs/"+jobID, "")
	if postRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusMethodNotAllowed, postRes.Code, postRes.Body.String())
	}
}
