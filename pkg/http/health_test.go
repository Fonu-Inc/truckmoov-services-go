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
		Status bool `json:"status"`
		Data   struct {
			Ready  bool              `json:"ready"`
			Checks map[string]string `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Status || !body.Data.Ready {
		t.Fatal("expected status=true and ready=true")
	}
	if body.Data.Checks["postgres"] != "ok" || body.Data.Checks["nats"] != "ok" {
		t.Fatalf("expected all checks ok, got %v", body.Data.Checks)
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

	var body ErrorBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status || body.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected a 503 error envelope, got %#v", body)
	}
	if len(body.Errors) != 1 || body.Errors[0] != "nats: connection refused" {
		t.Fatalf("expected only the nats failure listed, got %v", body.Errors)
	}
}
