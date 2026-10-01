package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/pkg/identity"
)

func TestWriteErrorFollowsTheContract(t *testing.T) {
	res := httptest.NewRecorder()

	WriteError(res, http.StatusBadRequest, "pickup address is required", "pickup address is required", "dropoff address is required")

	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != false || body["message"] != "pickup address is required" || body["error"] != "Bad Request" || body["statusCode"] != float64(400) {
		t.Fatalf("unexpected error body: %#v", body)
	}
	if errs, _ := body["errors"].([]any); len(errs) != 2 {
		t.Fatalf("expected both details, got %#v", body["errors"])
	}
	if res.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("expected json, got %q", res.Header().Get("Content-Type"))
	}
}

func TestWriteOKFollowsTheContract(t *testing.T) {
	res := httptest.NewRecorder()

	WriteOK(res, http.StatusCreated, map[string]string{"id": "job-1"})

	var body SuccessBody
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Code != http.StatusCreated || !body.Status || body.Message != "success" || body.Data == nil {
		t.Fatalf("unexpected success body: %d %#v", res.Code, body)
	}
}

// A refusal must not tell the caller a route exists, so it has to be the very
// same answer an unknown path gets.
func TestAnUnknownPathAndARefusalAnswerAlike(t *testing.T) {
	mux := http.NewServeMux()
	NewRoutes(mux, true, nil, nil).EnforcePermissions(true).HandleFunc("/jobs", []Audience{Tenant}, On("tenant.dispatch"), ok)
	demoted := &AuthClaims{Role: "DISPATCHER", TenantID: "t1", Portal: identity.PortalTenant, Permissions: []string{}}

	refused := httptest.NewRecorder()
	mux.ServeHTTP(refused, withClaims(httptest.NewRequest(http.MethodGet, "/jobs", nil), demoted))
	unknown := httptest.NewRecorder()
	mux.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/nothing-here", nil))

	if refused.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound {
		t.Fatalf("expected two 404s, got %d and %d", refused.Code, unknown.Code)
	}
	if refused.Body.String() != unknown.Body.String() {
		t.Fatalf("refusal and unknown path differ:\n%s\n%s", refused.Body.String(), unknown.Body.String())
	}
}
