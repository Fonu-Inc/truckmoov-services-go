package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func TestAssignSubDispatchJobUseCase_RefusesInactiveRelationship(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	createOffer := NewCreateJobOfferUseCase(offers, jobs, &recordingOfferPublisher{}, fixedOfferClock{now: time.Now()}, &sequentialOfferIDs{})
	useCase := NewAssignSubDispatchJobUseCase(relationships, createOffer, newFakeActivityRepository(), fixedOfferClock{now: time.Now()}, &sequentialAggregatorIDs{})

	_, err := useCase.Execute(context.Background(), AssignSubDispatchJobCommand{
		AggregatorTenantID: "aggregator-1", JobID: "job-1", OperatorTenantID: "operator-1", OfferedRateNgn: 1000,
	})
	if err != ErrSubDispatchRelationshipNotActive {
		t.Fatalf("expected ErrSubDispatchRelationshipNotActive, got %v", err)
	}
}

func TestAssignSubDispatchJobUseCase_CreatesOfferAndRecordsActivity(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	activity := newFakeActivityRepository()
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	rel, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	_, _ = relationships.Create(context.Background(), rel)

	job, _ := domain.NewJob(domain.Job{
		ID: "job-1", TenantID: "aggregator-1",
		Pickup: domain.JobStop{Address: "Lagos"}, Dropoff: domain.JobStop{Address: "Ibadan"},
		AmountNgn: 50000, CreatedAt: now, UpdatedAt: now,
	})
	_ = jobs.Save(job)

	createOffer := NewCreateJobOfferUseCase(offers, jobs, &recordingOfferPublisher{}, fixedOfferClock{now: now}, &sequentialOfferIDs{})
	useCase := NewAssignSubDispatchJobUseCase(relationships, createOffer, activity, fixedOfferClock{now: now}, &sequentialAggregatorIDs{})

	offer, err := useCase.Execute(context.Background(), AssignSubDispatchJobCommand{
		AggregatorTenantID: "aggregator-1", JobID: "job-1", OperatorTenantID: "operator-1", OfferedRateNgn: 40000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offer.Status != domain.OfferStatusPending {
		t.Fatalf("expected pending offer, got %q", offer.Status)
	}
	if len(activity.entries) != 1 {
		t.Fatalf("expected one activity entry, got %d", len(activity.entries))
	}
	if activity.entries[0].Type != domain.ActivitySubDispatched {
		t.Fatalf("expected sub_dispatched activity type, got %q", activity.entries[0].Type)
	}
}

func TestSubDispatchQueueUseCase_DerivesStatusAcrossLifecycle(t *testing.T) {
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	// Unassigned job still owned by the aggregator: PENDING_ASSIGNMENT.
	unassigned, _ := domain.NewJob(domain.Job{
		ID: "job-unassigned", TenantID: "aggregator-1",
		Pickup: domain.JobStop{Address: "Lagos"}, Dropoff: domain.JobStop{Address: "Abuja"},
		AmountNgn: 10000, CreatedAt: now, UpdatedAt: now,
	})
	_ = jobs.Save(unassigned)

	// Assigned: a pending offer exists.
	assigned, _ := domain.NewJob(domain.Job{
		ID: "job-assigned", TenantID: "aggregator-1",
		Pickup: domain.JobStop{Address: "Lagos"}, Dropoff: domain.JobStop{Address: "Kano"},
		AmountNgn: 20000, CreatedAt: now, UpdatedAt: now,
	})
	_ = jobs.Save(assigned)
	pendingOffer, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-assigned", JobID: "job-assigned", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 18000, Status: domain.OfferStatusPending, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	_ = offers.Save(context.Background(), pendingOffer)

	// In transit: offer accepted, job moved to operator, not delivered yet.
	inTransitJob, _ := domain.NewJob(domain.Job{
		ID: "job-transit", TenantID: "operator-1",
		Pickup: domain.JobStop{Address: "Lagos"}, Dropoff: domain.JobStop{Address: "Kaduna"},
		Status: domain.StatusEnRoute, AmountNgn: 30000, CreatedAt: now, UpdatedAt: now,
	})
	_ = jobs.Save(inTransitJob)
	acceptedOffer, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-transit", JobID: "job-transit", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 28000, Status: domain.OfferStatusAccepted, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	_ = offers.Save(context.Background(), acceptedOffer)

	useCase := NewSubDispatchQueueUseCase(offers, jobs)
	entries, err := useCase.Execute(context.Background(), "aggregator-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	byID := map[string]SubDispatchQueueEntry{}
	for _, e := range entries {
		byID[e.JobID] = e
	}

	if got := byID["job-unassigned"].Status; got != SubDispatchPendingAssignment {
		t.Fatalf("expected pending_assignment, got %q", got)
	}
	if got := byID["job-assigned"].Status; got != SubDispatchAssigned {
		t.Fatalf("expected assigned, got %q", got)
	}
	if got := byID["job-assigned"].OperatorID; got != "operator-1" {
		t.Fatalf("expected operator-1, got %q", got)
	}
	if got := byID["job-transit"].Status; got != SubDispatchInTransit {
		t.Fatalf("expected in_transit, got %q", got)
	}
}
