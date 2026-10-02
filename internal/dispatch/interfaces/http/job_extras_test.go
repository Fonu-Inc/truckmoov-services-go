package dispatchhttp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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

type extrasHarness struct {
	mux   *http.ServeMux
	jobs  *infrastructure.InMemoryJobRepository
	jobID string
}

func newExtrasHarness(t *testing.T, createBody string) extrasHarness {
	t.Helper()
	clock := fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	ids := &sequenceDispatchID{}
	h := extrasHarness{mux: http.NewServeMux(), jobs: infrastructure.NewInMemoryJobRepository()}
	publish := application.NewPublishJobStatusChangedUseCase(&fakeJobStatusPublisher{}, clock, ids, "default")
	lifecycle := application.NewJobLifecycleUseCase(h.jobs, publish, &countingInstructionNotifier{}, clock, ids, "default")
	handler := NewHandler(publish, lifecycle, nil, nil, nil, nil).
		WithJobAttachments(application.NewJobAttachmentUseCase(h.jobs, infrastructure.NewInMemoryJobAttachmentStore(), clock, ids))
	handler.RegisterRoutes(httpx.NewRoutes(h.mux, false, nil, nil))

	res := dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs", createBody)
	if res.Code != http.StatusCreated {
		t.Fatalf("create job: expected %d, got %d body=%s", http.StatusCreated, res.Code, res.Body.String())
	}
	h.jobID = decodeJobBody(t, res.Body.Bytes()).ID
	return h
}

func (h extrasHarness) stored(t *testing.T) domain.Job {
	t.Helper()
	job, err := h.jobs.FindByTenantAndID("default", h.jobID)
	if err != nil {
		t.Fatal("job missing")
	}
	return job
}

const plainJobBody = `{"pickup":{"address":"Apapa"},"dropoff":{"address":"Lekki"}}`

func TestCreateJobKeepsWizardFields(t *testing.T) {
	h := newExtrasHarness(t, `{
		"pickup":{"address":"Apapa","contact_name":"Ada","contact_phone":"+2348011111111","company_name":"Acme","date":"2026-03-01","time_from":"08:00","time_to":"10:00"},
		"dropoff":{"address":"Lekki","contact_name":"Bola"},
		"cargo":{"category":"Electronics","packages_count":4,"unit":"kg","value_ngn":250000,
			"dimensions":{"length_cm":120,"width_cm":80,"height_cm":60},
			"special_handling":["Fragile"],"equipment":["Forklift"]}
	}`)

	job := h.stored(t)

	if job.Category != "Electronics" || job.PackagesCount != 4 || job.ValueNgn != 250000 || job.Unit != "kg" {
		t.Fatalf("cargo not stored: %+v", job.JobCargoDetails)
	}
	if job.Dimensions == nil || job.Dimensions.LengthCm != 120 || len(job.SpecialHandling) != 1 || job.Equipment[0] != "Forklift" {
		t.Fatalf("cargo lists not stored: %+v", job.JobCargoDetails)
	}
	if job.Pickup.ContactName != "Ada" || job.Pickup.CompanyName != "Acme" || job.Pickup.TimeFrom != "08:00" || job.Dropoff.ContactName != "Bola" {
		t.Fatalf("stop contacts not stored: %+v %+v", job.Pickup, job.Dropoff)
	}
}

func TestUpdateJobReplacesCargoDetailsOnlyWhenSent(t *testing.T) {
	h := newExtrasHarness(t, `{"pickup":{"address":"Apapa"},"dropoff":{"address":"Lekki"},"cargo":{"category":"Food"}}`)

	res := dispatchRequest(t, h.mux, http.MethodPatch, "/dispatch/jobs/"+h.jobID, `{"amount":500}`)
	if res.Code != http.StatusOK || h.stored(t).Category != "Food" {
		t.Fatalf("a patch without cargo must keep it: %d %q", res.Code, h.stored(t).Category)
	}

	res = dispatchRequest(t, h.mux, http.MethodPatch, "/dispatch/jobs/"+h.jobID, `{"cargo":{"category":"Machinery","packages_count":2},"required_vehicle_type":"Flatbed"}`)
	if res.Code != http.StatusOK || h.stored(t).Category != "Machinery" || h.stored(t).PackagesCount != 2 || h.stored(t).RequiredVehicleType != "Flatbed" {
		t.Fatalf("expected cargo and vehicle type replaced: %d %+v", res.Code, h.stored(t))
	}
}

func notificationServiceRequest(t *testing.T, mux http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	verifier := identity.NewServiceTokenVerifier("dispatch", "dev-secret")
	handler := httpx.WithAuthContext(httpx.WithServiceCallers(mux, verifier, httpx.ServiceRoles{"notification": "DISPATCHER"}), identity.SharedSecretVerifier("replace-me"))
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identity.ServiceTokenHeader, identity.NewServiceTokens("notification", "", "dev-secret").For("dispatch"))
	req.Header.Set(identity.ActingForTenantHeader, "default")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestDriverInstructionOutcomeRoute(t *testing.T) {
	h := newExtrasHarness(t, plainJobBody)
	dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs/"+h.jobID+"/assign", `{"driver_id":"driver-1","driver_phone":"+2348012345678"}`)
	path := "/dispatch/jobs/" + h.jobID + "/driver-instruction-outcome"

	user := dispatchRequest(t, h.mux, http.MethodPost, path, `{"driver_id":"driver-1","outcome":"sent"}`)
	if user.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a user token, got %d", user.Code)
	}

	res := notificationServiceRequest(t, h.mux, path, `{"driver_id":"driver-1","outcome":"not_sent","reason":"no WhatsApp number"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	timeline := h.stored(t).Timeline
	last := timeline[len(timeline)-1]
	if last.Type != "driver_instruction_not_sent" || last.Metadata["reason"] != "no WhatsApp number" {
		t.Fatalf("unexpected timeline entry %+v", last)
	}

	stale := notificationServiceRequest(t, h.mux, path, `{"driver_id":"driver-9","outcome":"sent"}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a driver no longer assigned, got %d", stale.Code)
	}
	bad := notificationServiceRequest(t, h.mux, path, `{"driver_id":"driver-1","outcome":"maybe"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown outcome, got %d", bad.Code)
	}
}

func TestAttachmentUploadAndDownload(t *testing.T) {
	h := newExtrasHarness(t, plainJobBody)
	content := base64.StdEncoding.EncodeToString([]byte("bill of lading"))

	res := dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs/"+h.jobID+"/attachments",
		`{"name":"bol.txt","content_type":"text/plain","content_base64":"`+content+`"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d body=%s", res.Code, res.Body.String())
	}
	attachments := h.stored(t).Attachments
	if len(attachments) != 1 || attachments[0].Name != "bol.txt" || attachments[0].SizeBytes != 14 {
		t.Fatalf("unexpected attachments %+v", attachments)
	}

	download := dispatchRequest(t, h.mux, http.MethodGet, "/dispatch/jobs/"+h.jobID+"/attachments/"+attachments[0].ID, "")
	var envelope struct {
		Data struct {
			Name          string `json:"name"`
			ContentBase64 string `json:"content_base64"`
		} `json:"data"`
	}
	if err := json.Unmarshal(download.Body.Bytes(), &envelope); err != nil || download.Code != http.StatusOK {
		t.Fatalf("download: %d %v body=%s", download.Code, err, download.Body.String())
	}
	if envelope.Data.Name != "bol.txt" || envelope.Data.ContentBase64 != content {
		t.Fatalf("unexpected download %+v", envelope.Data)
	}
}

func TestAttachmentUploadRefusals(t *testing.T) {
	h := newExtrasHarness(t, plainJobBody)
	big := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", application.MaxJobAttachmentBytes+1)))

	cases := []struct {
		name string
		body string
		want int
	}{
		{"not base64", `{"name":"a.txt","content_base64":"***"}`, http.StatusBadRequest},
		{"no name", `{"content_base64":"YQ=="}`, http.StatusBadRequest},
		{"empty file", `{"name":"a.txt","content_base64":""}`, http.StatusBadRequest},
		{"too large", `{"name":"a.txt","content_base64":"` + big + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		res := dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs/"+h.jobID+"/attachments", tc.body)
		if res.Code != tc.want {
			t.Fatalf("%s: expected %d, got %d body=%.120s", tc.name, tc.want, res.Code, res.Body.String())
		}
	}
	missing := dispatchRequest(t, h.mux, http.MethodGet, "/dispatch/jobs/"+h.jobID+"/attachments/nope", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown attachment, got %d", missing.Code)
	}
}

func TestResendInstructionRoute(t *testing.T) {
	h := newExtrasHarness(t, plainJobBody)

	early := dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs/"+h.jobID+"/resend-instruction", "")
	if early.Code != http.StatusConflict {
		t.Fatalf("expected 409 before a driver is assigned, got %d body=%s", early.Code, early.Body.String())
	}

	dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs/"+h.jobID+"/assign", `{"driver_id":"driver-1","driver_phone":"+2348012345678"}`)
	res := dispatchRequest(t, h.mux, http.MethodPost, "/dispatch/jobs/"+h.jobID+"/resend-instruction", "")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
}
