package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithCORSAllowsConfiguredOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := WithCORS(next, []string{"http://localhost:5173"})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("expected allow-origin header to match request origin, got %q", got)
	}
}

func TestWithCORSDoesNotExposeDisallowedOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := WithCORS(next, []string{"http://localhost:5173"})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Origin", "https://example.com")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no allow-origin header for disallowed origin, got %q", got)
	}
}

func TestWithCORSHandlesPreflight(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := WithCORS(next, []string{"http://localhost:5173"})
	request := httptest.NewRequest(http.MethodOptions, "/health", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, response.Code)
	}

	if nextCalled {
		t.Fatal("expected middleware to short-circuit preflight request")
	}
}

func readBodyHandler(raise int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raise > 0 {
			LimitBody(w, r, raise)
		}
		if _, err := io.ReadAll(r.Body); err != nil {
			WriteError(w, http.StatusRequestEntityTooLarge, "too large")
			return
		}
		WriteOK(w, http.StatusOK, nil)
	})
}

func postBody(handler http.Handler, size int) int {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(strings.Repeat("x", size))))
	return recorder.Code
}

func TestLimitBodyRaisesTheDefaultCapForOneRoute(t *testing.T) {
	handler := WithMaxBodyBytes(readBodyHandler(4096), 1024)

	if code := postBody(handler, 3000); code != http.StatusOK {
		t.Fatalf("a body under the route's own limit must get past the default cap, got %d", code)
	}
	if code := postBody(handler, 5000); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body over the route's own limit must be refused, got %d", code)
	}
}

func TestDefaultBodyCapAppliesWithoutLimitBody(t *testing.T) {
	handler := WithMaxBodyBytes(readBodyHandler(0), 1024)

	if code := postBody(handler, 3000); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected the default cap to refuse the body, got %d", code)
	}
}

func TestWithAPIKeyAcceptsOnlyTheHeader(t *testing.T) {
	handler := WithAPIKey(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "secret", false)

	cases := map[string]func(*http.Request){
		"header": func(r *http.Request) { r.Header.Set("X-Api-Key", "secret") },
		"query":  func(r *http.Request) { r.URL.RawQuery = "api_key=secret" },
	}
	want := map[string]int{"header": http.StatusOK, "query": http.StatusUnauthorized}
	for name, apply := range cases {
		req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
		apply(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want[name] {
			t.Fatalf("%s: expected %d, got %d", name, want[name], rec.Code)
		}
	}
}
