package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandlerRespondsOK(t *testing.T) {
	metrics := NewMetrics("test")

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	metrics.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
}

func TestMetricsMiddlewareRecordsRequest(t *testing.T) {
	metrics := NewMetrics("test")

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/metrics", metrics.Handler())
	handler := metrics.Middleware(mux)

	pingReq := httptest.NewRequest(http.MethodGet, "/ping", nil)
	pingRes := httptest.NewRecorder()
	handler.ServeHTTP(pingRes, pingReq)
	if pingRes.Code != http.StatusNoContent {
		t.Fatalf("expected downstream status %d, got %d", http.StatusNoContent, pingRes.Code)
	}

	scrapeReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	scrapeRes := httptest.NewRecorder()
	handler.ServeHTTP(scrapeRes, scrapeReq)
	if scrapeRes.Code != http.StatusOK {
		t.Fatalf("expected metrics status %d, got %d", http.StatusOK, scrapeRes.Code)
	}

	body := scrapeRes.Body.String()
	if !strings.Contains(body, "http_requests_total") {
		t.Fatal("expected http_requests_total to be registered")
	}
	if !strings.Contains(body, "http_request_duration_seconds") {
		t.Fatal("expected http_request_duration_seconds to be registered")
	}
	// Route label must be the matched pattern, not the raw path, to keep
	// cardinality bounded.
	if !strings.Contains(body, `route="/ping"`) {
		t.Fatalf("expected route label of matched pattern, got body:\n%s", body)
	}
}
