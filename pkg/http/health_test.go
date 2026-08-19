package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadinessHandlerAllDepsHealthy(t *testing.T) {
	handler := ReadinessHandler("svc",
		ReadinessCheck{Name: "postgres", Probe: func(context.Context) error { return nil }},
		ReadinessCheck{Name: "nats", Probe: func(context.Context) error { return nil }},
	)

	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	var body struct {
		Ready  bool              `json:"ready"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Ready {
		t.Fatal("expected ready=true")
	}
	if body.Checks["postgres"] != "ok" || body.Checks["nats"] != "ok" {
		t.Fatalf("expected all checks ok, got %v", body.Checks)
	}
}

func TestReadinessHandlerFailingDepReturns503(t *testing.T) {
	handler := ReadinessHandler("svc",
		ReadinessCheck{Name: "postgres", Probe: func(context.Context) error { return nil }},
		ReadinessCheck{Name: "nats", Probe: func(context.Context) error { return errors.New("connection refused") }},
	)

	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, response.Code)
	}

	var body struct {
		Ready  bool              `json:"ready"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Ready {
		t.Fatal("expected ready=false")
	}
	if body.Checks["postgres"] != "ok" {
		t.Fatalf("expected postgres ok, got %q", body.Checks["postgres"])
	}
	if body.Checks["nats"] != "connection refused" {
		t.Fatalf("expected nats failure reason, got %q", body.Checks["nats"])
	}
}
