package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/truckmoov/services-go/pkg/alert"
)

func TestErrorAlertSeesTenantSetByInnerAuthMiddleware(t *testing.T) {
	recent := NewRecentRequests(3)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, http.StatusBadRequest, "bad")
	})
	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(setClaims(r.Context(), &AuthClaims{Subject: "u1", TenantID: "t-77"}))
		inner.ServeHTTP(w, r)
	})
	handler := WithErrorAlerts(authed, alert.NewNtfyClient("", ""), recent, "svc")
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/dispatch/jobs", nil))
	snapshot := recent.Snapshot()
	if len(snapshot) != 1 || snapshot[0].TenantID != "t-77" {
		t.Fatalf("expected tenant t-77 recorded, got %+v", snapshot)
	}
}

func TestErrorAlertRecordsTheRegisteredPatternNotTheCollapsedPath(t *testing.T) {
	recent := NewRecentRequests(3)
	mux := http.NewServeMux()
	mux.HandleFunc("/dispatch/jobs/{id}/status", func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusBadRequest, "bad")
	})
	copied := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		AlertRoute(mux).ServeHTTP(w, r.Clone(r.Context()))
	})
	handler := WithErrorAlerts(copied, alert.NewNtfyClient("", ""), recent, "svc")

	request := httptest.NewRequest(http.MethodGet, "/dispatch/jobs/job-1/status", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	snapshot := recent.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Path != "/dispatch/jobs/{id}/status" {
		t.Fatalf("expected the mux pattern recorded, got %+v", snapshot)
	}
}
