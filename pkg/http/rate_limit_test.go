package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func doRequest(handler http.Handler, ip string) int {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if ip != "" {
		req.RemoteAddr = ip + ":1234"
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res.Code
}

func TestWithRateLimit_AllowsRequestsWithinBurst(t *testing.T) {
	handler := WithRateLimit(okHandler(), 10, 5)
	for i := 0; i < 5; i++ {
		if code := doRequest(handler, "1.2.3.4"); code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, code)
		}
	}
}

func TestWithRateLimit_BlocksAfterBurstExhausted(t *testing.T) {
	handler := WithRateLimit(okHandler(), 1, 3)
	for i := 0; i < 3; i++ {
		doRequest(handler, "1.2.3.5")
	}
	if code := doRequest(handler, "1.2.3.5"); code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst exhausted, got %d", code)
	}
}

func TestWithRateLimit_SeparateBucketPerIP(t *testing.T) {
	handler := WithRateLimit(okHandler(), 1, 2)
	for i := 0; i < 2; i++ {
		doRequest(handler, "10.0.0.1")
	}
	// Different IP should still have a full bucket
	if code := doRequest(handler, "10.0.0.2"); code != http.StatusOK {
		t.Fatalf("expected 200 for new IP, got %d", code)
	}
}

func TestWithRateLimit_ZeroRPS_IsNoOp(t *testing.T) {
	handler := WithRateLimit(okHandler(), 0, 10)
	for i := 0; i < 1000; i++ {
		if code := doRequest(handler, "1.2.3.6"); code != http.StatusOK {
			t.Fatalf("expected 200 with disabled rate limit, got %d", code)
		}
	}
}

func TestWithRateLimit_XForwardedForUsedAsIP(t *testing.T) {
	handler := WithRateLimit(okHandler(), 1, 1)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "5.5.5.5")
	req.RemoteAddr = "127.0.0.1:9999"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	// second request same forwarded IP exhausts burst of 1
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Forwarded-For", "5.5.5.5")
	req2.RemoteAddr = "127.0.0.1:9999"
	res2 := httptest.NewRecorder()
	handler.ServeHTTP(res2, req2)
	if res2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst, got %d", res2.Code)
	}
}

func TestWithRateLimit_IngestPathBypassesLimit(t *testing.T) {
	handler := WithRateLimit(okHandler(), 1, 1)
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodPost, "/tracking/ingest", nil)
		req.RemoteAddr = "9.9.9.9:1234"
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("ingest request %d: expected 200, got %d", i+1, res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest/vendor/teltonika", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("vendor ingest: expected 200, got %d", res.Code)
	}
}
