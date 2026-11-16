package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func deliveredJobFixture(t *testing.T, jobs *lifecycleRepository, id, tenantID string, now time.Time) domain.Job {
	t.Helper()
	job, err := domain.NewJob(domain.Job{
		ID: id, TenantID: tenantID,
		Pickup: domain.JobStop{Address: "Lagos"}, Dropoff: domain.JobStop{Address: "Ibadan"},
		Status: domain.StatusDelivered, AmountNgn: 10000, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected error building job fixture: %v", err)
	}
	_ = jobs.Save(job)
	return job
}

func linkedRelationships(t *testing.T, aggregatorTenantID, operatorTenantID string, status domain.RelationshipStatus) *fakeRelationshipRepository {
	t.Helper()
	repo := newFakeRelationshipRepository()
	now := time.Now()
	rel, err := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: aggregatorTenantID, OperatorTenantID: operatorTenantID, Status: status,
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected error building relationship fixture: %v", err)
	}
	_, _ = repo.Create(context.Background(), rel)
	return repo
}

func TestRateOperatorUseCase_Succeeds(t *testing.T) {
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	ratings := newFakeRatingRepository()
	now := time.Now()

	deliveredJobFixture(t, jobs, "job-1", "operator-1", now)
	accepted, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 9000, Status: domain.OfferStatusAccepted, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	_ = offers.Save(context.Background(), accepted)

	useCase := NewRateOperatorUseCase(linkedRelationships(t, "aggregator-1", "operator-1", domain.RelationshipStatusActive), offers, jobs, ratings, fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	rating, err := useCase.Execute(context.Background(), RateOperatorCommand{
		RaterTenantID: "aggregator-1", OperatorTenantID: "operator-1", JobID: "job-1", Value: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rating.Value != 5 {
		t.Fatalf("expected value 5, got %d", rating.Value)
	}
}

func TestRateOperatorUseCase_RefusesDuplicateRating(t *testing.T) {
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	ratings := newFakeRatingRepository()
	now := time.Now()

	deliveredJobFixture(t, jobs, "job-1", "operator-1", now)
	accepted, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 9000, Status: domain.OfferStatusAccepted, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	_ = offers.Save(context.Background(), accepted)

	useCase := NewRateOperatorUseCase(linkedRelationships(t, "aggregator-1", "operator-1", domain.RelationshipStatusActive), offers, jobs, ratings, fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	cmd := RateOperatorCommand{RaterTenantID: "aggregator-1", OperatorTenantID: "operator-1", JobID: "job-1", Value: 5}
	if _, err := useCase.Execute(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error on first rating: %v", err)
	}
	if _, err := useCase.Execute(context.Background(), cmd); err != ErrRatingAlreadyExists {
		t.Fatalf("expected ErrRatingAlreadyExists, got %v", err)
	}
}

func TestRateOperatorUseCase_RefusesUndeliveredJob(t *testing.T) {
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	now := time.Now()

	job, _ := domain.NewJob(domain.Job{
		ID: "job-1", TenantID: "operator-1",
		Pickup: domain.JobStop{Address: "Lagos"}, Dropoff: domain.JobStop{Address: "Ibadan"},
		Status: domain.StatusEnRoute, CreatedAt: now, UpdatedAt: now,
	})
	_ = jobs.Save(job)
	accepted, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 9000, Status: domain.OfferStatusAccepted, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	_ = offers.Save(context.Background(), accepted)

	useCase := NewRateOperatorUseCase(linkedRelationships(t, "aggregator-1", "operator-1", domain.RelationshipStatusActive), offers, jobs, newFakeRatingRepository(), fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	_, err := useCase.Execute(context.Background(), RateOperatorCommand{
		RaterTenantID: "aggregator-1", OperatorTenantID: "operator-1", JobID: "job-1", Value: 5,
	})
	if err != ErrJobNotDelivered {
		t.Fatalf("expected ErrJobNotDelivered, got %v", err)
	}
}

func TestRateOperatorUseCase_RefusesJobNotSubDispatchedByThisRater(t *testing.T) {
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	now := time.Now()

	deliveredJobFixture(t, jobs, "job-1", "operator-1", now)
	// Sub-dispatched by a DIFFERENT aggregator.
	accepted, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "other-aggregator", TargetTenantID: "operator-1",
		OfferedRateNgn: 9000, Status: domain.OfferStatusAccepted, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	_ = offers.Save(context.Background(), accepted)

	useCase := NewRateOperatorUseCase(linkedRelationships(t, "aggregator-1", "operator-1", domain.RelationshipStatusActive), offers, jobs, newFakeRatingRepository(), fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	_, err := useCase.Execute(context.Background(), RateOperatorCommand{
		RaterTenantID: "aggregator-1", OperatorTenantID: "operator-1", JobID: "job-1", Value: 5,
	})
	if err != ErrSubDispatchJobNotFound {
		t.Fatalf("expected ErrSubDispatchJobNotFound, got %v", err)
	}
}

func TestRateOperatorUseCase_SameNotFoundWithoutAcceptedRelationship(t *testing.T) {
	now := time.Now()
	for _, status := range []domain.RelationshipStatus{domain.RelationshipStatusPending, ""} {
		jobs := newLifecycleRepository()
		offers := newFakeOfferRepository(jobs)
		job, _ := domain.NewJob(domain.Job{
			ID: "job-1", TenantID: "operator-1",
			Pickup: domain.JobStop{Address: "Lagos"}, Dropoff: domain.JobStop{Address: "Ibadan"},
			Status: domain.StatusEnRoute, CreatedAt: now, UpdatedAt: now,
		})
		_ = jobs.Save(job)

		relationships := newFakeRelationshipRepository()
		if status != "" {
			relationships = linkedRelationships(t, "aggregator-1", "operator-1", status)
		}
		useCase := NewRateOperatorUseCase(relationships, offers, jobs, newFakeRatingRepository(), fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
		_, err := useCase.Execute(context.Background(), RateOperatorCommand{
			RaterTenantID: "aggregator-1", OperatorTenantID: "operator-1", JobID: "job-1", Value: 5,
		})
		if err != ErrSubDispatchJobNotFound {
			t.Fatalf("status %q: expected ErrSubDispatchJobNotFound, got %v", status, err)
		}
	}
}

func TestRateOperatorUseCase_AllowsEndedRelationship(t *testing.T) {
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	now := time.Now()

	deliveredJobFixture(t, jobs, "job-1", "operator-1", now)
	accepted, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 9000, Status: domain.OfferStatusAccepted, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	_ = offers.Save(context.Background(), accepted)

	relationships := linkedRelationships(t, "aggregator-1", "operator-1", domain.RelationshipStatusEnded)
	useCase := NewRateOperatorUseCase(relationships, offers, jobs, newFakeRatingRepository(), fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	if _, err := useCase.Execute(context.Background(), RateOperatorCommand{
		RaterTenantID: "aggregator-1", OperatorTenantID: "operator-1", JobID: "job-1", Value: 4,
	}); err != nil {
		t.Fatalf("unexpected error rating after the link ended: %v", err)
	}
}
