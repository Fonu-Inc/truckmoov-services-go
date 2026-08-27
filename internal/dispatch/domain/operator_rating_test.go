package domain

import (
	"testing"
	"time"
)

func TestNewOperatorRating_Valid(t *testing.T) {
	now := time.Now()
	rating, err := NewOperatorRating(OperatorRating{
		ID: "rating-1", JobID: "job-1", RaterTenantID: "aggregator-1",
		OperatorTenantID: "operator-1", Value: 4, Note: "on time", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rating.Value != 4 {
		t.Fatalf("expected value 4, got %d", rating.Value)
	}
}

func TestNewOperatorRating_RejectsOutOfRangeValue(t *testing.T) {
	now := time.Now()
	cases := []int{0, -1, 6, 100}
	for _, v := range cases {
		_, err := NewOperatorRating(OperatorRating{
			ID: "rating-1", JobID: "job-1", RaterTenantID: "aggregator-1",
			OperatorTenantID: "operator-1", Value: v, CreatedAt: now,
		})
		if err == nil {
			t.Fatalf("expected error for value %d", v)
		}
	}
}

func TestNewOperatorRating_RejectsSelfRating(t *testing.T) {
	now := time.Now()
	_, err := NewOperatorRating(OperatorRating{
		ID: "rating-1", JobID: "job-1", RaterTenantID: "tenant-1",
		OperatorTenantID: "tenant-1", Value: 5, CreatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error when rater and operator are the same tenant")
	}
}

func TestNewOperatorRating_RequiresIDs(t *testing.T) {
	now := time.Now()
	base := OperatorRating{
		ID: "rating-1", JobID: "job-1", RaterTenantID: "aggregator-1",
		OperatorTenantID: "operator-1", Value: 5, CreatedAt: now,
	}

	missingID := base
	missingID.ID = ""
	if _, err := NewOperatorRating(missingID); err == nil {
		t.Fatal("expected error for missing id")
	}

	missingJob := base
	missingJob.JobID = ""
	if _, err := NewOperatorRating(missingJob); err == nil {
		t.Fatal("expected error for missing job id")
	}
}
