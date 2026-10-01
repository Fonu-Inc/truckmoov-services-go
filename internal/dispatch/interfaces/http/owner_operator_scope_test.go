package dispatchhttp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/internal/dispatch/infrastructure"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

type fakeOwnedVehicles struct {
	byOperator map[string][]string
	err        error
}

func (f fakeOwnedVehicles) OwnedVehicleIDs(_ context.Context, _, ownerOperatorID string) ([]string, error) {
	return f.byOperator[ownerOperatorID], f.err
}

var twoOperatorsVehicles = fakeOwnedVehicles{byOperator: map[string][]string{
	"oo-a": {"v-a", "v-a2"},
	"oo-b": {"v-b"},
}}

func scopedDispatchServer(t *testing.T, owned ownedVehicleLister, wrap func(http.Handler) http.Handler) http.Handler {
	t.Helper()
	clock := fixedDispatchClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	ids := &sequenceDispatchID{}
	repository := infrastructure.NewInMemoryJobRepository()
	for _, job := range []struct{ id, vehicle string }{{"j-a", "v-a"}, {"j-b", "v-b"}, {"j-fleet", "v-fleet"}, {"j-pool", ""}} {
		status := domain.StatusAssigned
		if job.vehicle == "" {
			status = domain.StatusCreated
		}
		if err := repository.Save(domain.Job{
			ID: job.id, TenantID: "default", Status: status, AssignedVehicleID: job.vehicle, AssignedDriverID: "d-" + job.id,
			Pickup: domain.JobStop{Address: "Apapa"}, Dropoff: domain.JobStop{Address: "Lekki"}, CreatedAt: clock.now, UpdatedAt: clock.now,
		}); err != nil {
			t.Fatalf("seed job: %v", err)
		}
	}
	publish := application.NewPublishJobStatusChangedUseCase(&fakeJobStatusPublisher{}, clock, ids, "default")
	lifecycle := application.NewJobLifecycleUseCase(repository, publish, &fakeDriverInstructionNotifier{}, clock, ids, "default")
	handler := NewHandler(publish, lifecycle, application.NewRoutingUseCase(repository, clock, "default"), nil, nil, nil).
		WithActiveJobsMap(application.NewActiveJobsMapUseCase(repository, clock, "default", 0, nil))
	handler.ownedVehicles = owned

	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	var server http.Handler = mux
	if wrap != nil {
		server = wrap(server)
	}
	return httpx.WithAuthContext(server, identity.SharedSecretVerifier("replace-me"))
}

func bearerFor(subject, role string) map[string]string {
	payload := fmt.Sprintf(`{"sub":"%s","email":"%s@example.com","role":"%s","tenantId":"default","exp":%d}`, subject, subject, role, time.Now().Add(time.Hour).Unix())
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, []byte("replace-me"))
	_, _ = mac.Write([]byte(encoded))
	return map[string]string{"Authorization": "Bearer " + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}
}

func listedJobIDs(t *testing.T, server http.Handler, auth map[string]string) []string {
	t.Helper()
	res := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs", "", auth)
	if res.Code != http.StatusOK {
		t.Fatalf("list jobs: %d %s", res.Code, res.Body.String())
	}
	var jobs []domain.Job
	decodeData(t, res.Body.Bytes(), &jobs)
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	slices.Sort(ids)
	return ids
}

func summaryTotal(t *testing.T, server http.Handler, auth map[string]string) int {
	t.Helper()
	res := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/summary", "", auth)
	var summary application.JobStatusSummary
	decodeData(t, res.Body.Bytes(), &summary)
	return summary.Total
}

func mapJobIDs(t *testing.T, server http.Handler, auth map[string]string) []string {
	t.Helper()
	res := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/map", "", auth)
	var result application.ActiveJobsMap
	decodeData(t, res.Body.Bytes(), &result)
	ids := []string{}
	for _, job := range result.Jobs {
		ids = append(ids, job.JobID)
	}
	return ids
}

func expectJobNotFound(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "job not found") {
		t.Fatalf("expected 404 job not found, got %d %s", res.Code, res.Body.String())
	}
}

func TestOwnerOperatorsOnOneTenantEachSeeOnlyJobsOnTheirOwnVehicles(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)

	for operator, want := range map[string]string{"oo-a": "j-a", "oo-b": "j-b"} {
		auth := bearerFor(operator, "OWNER_OPERATOR")
		if got := listedJobIDs(t, server, auth); !slices.Equal(got, []string{want}) {
			t.Fatalf("%s list: expected [%s], got %v", operator, want, got)
		}
		if got := summaryTotal(t, server, auth); got != 1 {
			t.Fatalf("%s summary: expected 1 job, got %d", operator, got)
		}
		if got := mapJobIDs(t, server, auth); !slices.Equal(got, []string{want}) {
			t.Fatalf("%s map: expected [%s], got %v", operator, want, got)
		}
	}
}

func TestAForeignOrUnassignedJobIsNotFoundToAnOwnerOperator(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)
	auth := bearerFor("oo-a", "OWNER_OPERATOR")

	expectJobNotFound(t, dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/j-b", "", auth))
	expectJobNotFound(t, dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/j-pool", "", auth))
	expectJobNotFound(t, dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/jobs/j-b/cancel", `{"reason":"x"}`, auth))
	expectJobNotFound(t, dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/j-b/timeline", "", auth))
	expectJobNotFound(t, dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/jobs/status", `{"job_id":"j-b","new_status":"en_route"}`, auth))
	expectJobNotFound(t, dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/routes/optimize", `{"job_ids":["j-a","j-b"],"origin":{"latitude":6.4,"longitude":3.4}}`, auth))

	if res := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/j-a", "", auth); res.Code != http.StatusOK {
		t.Fatalf("own job: expected 200, got %d %s", res.Code, res.Body.String())
	}
}

func TestAnOwnerOperatorCanAssignOnlyTheirOwnVehicles(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)
	auth := bearerFor("oo-a", "OWNER_OPERATOR")

	res := dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/jobs/j-a/assign", `{"driver_id":"d-1","vehicle_id":"v-b"}`, auth)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "vehicle not found") {
		t.Fatalf("foreign vehicle: expected 404 vehicle not found, got %d %s", res.Code, res.Body.String())
	}

	res = dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/jobs/j-a/assign", `{"driver_id":"d-1","vehicle_id":"v-a2"}`, auth)
	if res.Code != http.StatusOK || decodeJobBody(t, res.Body.Bytes()).AssignedVehicleID != "v-a2" {
		t.Fatalf("own vehicle: expected 200 on v-a2, got %d %s", res.Code, res.Body.String())
	}
}

func TestAnOwnerOperatorIsRefusedTenantWideRoutes(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)
	auth := bearerFor("oo-a", "OWNER_OPERATOR")

	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, "/dispatch/job-offers", ""},
		{http.MethodGet, "/dispatch/aggregator/summary", ""},
	} {
		if res := dispatchRequestWithHeaders(t, server, call.method, call.path, call.body, auth); res.Code != http.StatusNotFound {
			t.Fatalf("%s %s: expected 404, got %d %s", call.method, call.path, res.Code, res.Body.String())
		}
	}
}

func createJobAs(t *testing.T, server http.Handler, auth map[string]string, body string) domain.Job {
	t.Helper()
	res := dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/jobs", body, auth)
	if res.Code != http.StatusCreated {
		t.Fatalf("create job: expected 201, got %d %s", res.Code, res.Body.String())
	}
	return decodeJobBody(t, res.Body.Bytes())
}

func TestAnOwnerOperatorSeesTheUnassignedJobTheyCreated(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)
	auth := bearerFor("oo-a", "OWNER_OPERATOR")

	job := createJobAs(t, server, auth, `{"job_id":"j-own","pickupLocation":"Apapa","deliveryLocation":"Lekki"}`)
	if job.CreatedBy == nil || *job.CreatedBy != "oo-a" {
		t.Fatalf("expected created_by oo-a, got %v", job.CreatedBy)
	}

	if got := listedJobIDs(t, server, auth); !slices.Equal(got, []string{"j-a", "j-own"}) {
		t.Fatalf("list: expected [j-a j-own], got %v", got)
	}
	if got := summaryTotal(t, server, auth); got != 2 {
		t.Fatalf("summary: expected 2 jobs, got %d", got)
	}
	res := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/j-own", "", auth)
	if res.Code != http.StatusOK || *decodeJobBody(t, res.Body.Bytes()).CreatedBy != "oo-a" {
		t.Fatalf("get own job: expected 200 created by oo-a, got %d %s", res.Code, res.Body.String())
	}
}

func TestAJobAnOwnerOperatorCreatedIsNotFoundToAnotherOwnerOperator(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)
	createJobAs(t, server, bearerFor("oo-a", "OWNER_OPERATOR"), `{"job_id":"j-own","pickupLocation":"Apapa","deliveryLocation":"Lekki"}`)
	other := bearerFor("oo-b", "OWNER_OPERATOR")

	expectJobNotFound(t, dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/j-own", "", other))
	if got := listedJobIDs(t, server, other); !slices.Equal(got, []string{"j-b"}) {
		t.Fatalf("other operator list: expected [j-b], got %v", got)
	}
}

func TestAnOwnerOperatorAssignsTheirCreatedJobOnlyToTheirOwnVehicles(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)
	auth := bearerFor("oo-a", "OWNER_OPERATOR")
	createJobAs(t, server, auth, `{"job_id":"j-own","pickupLocation":"Apapa","deliveryLocation":"Lekki"}`)

	res := dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/jobs/j-own/assign", `{"vehicle_id":"v-b"}`, auth)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "vehicle not found") {
		t.Fatalf("foreign vehicle: expected 404 vehicle not found, got %d %s", res.Code, res.Body.String())
	}
	res = dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/jobs/j-own/assign", `{"vehicle_id":"v-a"}`, auth)
	if res.Code != http.StatusOK {
		t.Fatalf("own vehicle: expected 200, got %d %s", res.Code, res.Body.String())
	}
}

func TestCreatingAJobWithAnIDInUseIsRefused(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)

	res := dispatchRequestWithHeaders(t, server, http.MethodPost, "/dispatch/jobs", `{"job_id":"j-b","pickupLocation":"Apapa","deliveryLocation":"Lekki"}`, bearerFor("oo-a", "OWNER_OPERATOR"))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", res.Code, res.Body.String())
	}
	if got := listedJobIDs(t, server, bearerFor("oo-b", "OWNER_OPERATOR")); !slices.Equal(got, []string{"j-b"}) {
		t.Fatalf("j-b must stay with oo-b, got %v", got)
	}
}

func TestAnAdminSeesOwnerOperatorJobsAndIsRecordedAsCreator(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)
	createJobAs(t, server, bearerFor("oo-a", "OWNER_OPERATOR"), `{"job_id":"j-own","pickupLocation":"Apapa","deliveryLocation":"Lekki"}`)
	admin := bearerFor("staff-1", "FLEET_ADMIN")

	job := createJobAs(t, server, admin, `{"job_id":"j-admin","pickupLocation":"Apapa","deliveryLocation":"Lekki"}`)
	if job.CreatedBy == nil || *job.CreatedBy != "staff-1" {
		t.Fatalf("expected created_by staff-1, got %v", job.CreatedBy)
	}
	if got := listedJobIDs(t, server, admin); !slices.Equal(got, []string{"j-a", "j-admin", "j-b", "j-fleet", "j-own", "j-pool"}) {
		t.Fatalf("admin: expected every job, got %v", got)
	}
}

func TestAdminsAndDispatchersStillSeeTheWholeTenant(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, nil)
	everyJob := []string{"j-a", "j-b", "j-fleet", "j-pool"}

	for _, role := range []string{"FLEET_ADMIN", "DISPATCHER"} {
		auth := bearerFor("staff-1", role)
		if got := listedJobIDs(t, server, auth); !slices.Equal(got, everyJob) {
			t.Fatalf("%s: expected every job, got %v", role, got)
		}
		if res := dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/j-b", "", auth); res.Code != http.StatusOK {
			t.Fatalf("%s: expected 200 on j-b, got %d", role, res.Code)
		}
	}
}

func TestAFailedVehicleLookupLeavesAnOwnerOperatorOwningNothing(t *testing.T) {
	server := scopedDispatchServer(t, fakeOwnedVehicles{err: errors.New("fleet down")}, nil)
	auth := bearerFor("oo-a", "OWNER_OPERATOR")

	if got := listedJobIDs(t, server, auth); len(got) != 0 {
		t.Fatalf("expected no jobs when the lookup fails, got %v", got)
	}
	expectJobNotFound(t, dispatchRequestWithHeaders(t, server, http.MethodGet, "/dispatch/jobs/j-a", "", auth))
}

func organisationTypeFromAuth(t *testing.T, organizationType string) func(http.Handler) http.Handler {
	t.Helper()
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": identity.Access{UserID: "oo-a", Active: true, OrganizationType: organizationType}})
	}))
	t.Cleanup(auth.Close)
	client := identity.NewAccessClient(auth.URL, identity.NewCaller("dispatch", identity.NewServiceTokens("dispatch", t.TempDir(), "dev"), ""))
	return func(next http.Handler) http.Handler { return httpx.WithCurrentAccess(next, client) }
}

func TestTheFounderOfAnOwnerOperatorOrganisationSeesTheWholeTenant(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, organisationTypeFromAuth(t, "owner_operator"))

	if got := listedJobIDs(t, server, bearerFor("oo-a", "OWNER_OPERATOR")); len(got) != 4 {
		t.Fatalf("founder: expected every job, got %v", got)
	}
}

func TestAnOwnerOperatorInAnEnterpriseIsLimited(t *testing.T) {
	server := scopedDispatchServer(t, twoOperatorsVehicles, organisationTypeFromAuth(t, "enterprise"))

	if got := listedJobIDs(t, server, bearerFor("oo-a", "OWNER_OPERATOR")); !slices.Equal(got, []string{"j-a"}) {
		t.Fatalf("enterprise operator: expected [j-a], got %v", got)
	}
}
