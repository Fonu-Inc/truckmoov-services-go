package dispatchhttp

import (
	"encoding/json"
	"testing"
)

func decodeSchedulingRequest(t *testing.T, body string) updateJobSchedulingRequest {
	t.Helper()
	var req updateJobSchedulingRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return req
}

func TestSchedulingRequestTimeWindowTriState(t *testing.T) {
	absent, err := decodeSchedulingRequest(t, `{"priority":"high"}`).timeWindow()
	if err != nil || absent != nil {
		t.Fatalf("absent window must stay untouched, got %v %v", absent, err)
	}

	cleared, err := decodeSchedulingRequest(t, `{"time_window":null}`).timeWindow()
	if err != nil || cleared == nil || cleared.PickupStart != nil {
		t.Fatalf("null window must clear, got %v %v", cleared, err)
	}

	set, err := decodeSchedulingRequest(t, `{"time_window":{"pickup_start":"2026-03-01T08:00:00Z"}}`).timeWindow()
	if err != nil || set == nil || set.PickupStart == nil {
		t.Fatalf("value window must be parsed, got %v %v", set, err)
	}

	if _, err := decodeSchedulingRequest(t, `{"time_window":{"bogus":1}}`).timeWindow(); err == nil {
		t.Fatal("unknown window field must be refused")
	}
}
