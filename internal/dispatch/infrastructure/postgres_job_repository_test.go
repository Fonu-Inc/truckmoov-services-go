package infrastructure

import (
	"testing"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func TestDecodeStoredJob_NormalizesLegacyPriority(t *testing.T) {
	payload := []byte(`{
		"id":"job-1",
		"tenant_id":"tenant-1",
		"pickup":{"address":"Apapa"},
		"dropoff":{"address":"Lekki"},
		"priority":"critical",
		"status":"created",
		"created_at":"2026-04-01T10:00:00Z",
		"updated_at":"2026-04-01T10:00:00Z"
	}`)

	job, err := decodeStoredJob(payload)
	if err != nil {
		t.Fatalf("decodeStoredJob returned error: %v", err)
	}
	if job.Priority != domain.PriorityUrgent {
		t.Fatalf("expected legacy critical priority to normalize to %q, got %q", domain.PriorityUrgent, job.Priority)
	}
}
