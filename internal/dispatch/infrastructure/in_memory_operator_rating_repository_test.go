package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func TestInMemoryRatingsDoNotCollideAcrossTenantsOnSameJobID(t *testing.T) {
	repo := NewInMemoryOperatorRatingRepository()
	ctx := context.Background()
	now := time.Now()
	for _, operator := range []string{"op-a", "op-b"} {
		_, _ = repo.Create(ctx, domain.OperatorRating{ID: operator, JobID: "job-1", RaterTenantID: "agg", OperatorTenantID: operator, Value: 5, CreatedAt: now})
	}

	for _, operator := range []string{"op-a", "op-b"} {
		rating, found, _ := repo.FindByJob(ctx, operator, "job-1")
		if !found || rating.OperatorTenantID != operator {
			t.Fatalf("expected rating for %s, got %+v found=%v", operator, rating, found)
		}
	}
	if _, found, _ := repo.FindByJob(ctx, "op-c", "job-1"); found {
		t.Fatal("unrelated tenant must not see the rating")
	}
}
