package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func TestAddOperatorUseCase_DirectLinkWaitsForOperatorAcceptance(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	activity := newFakeActivityRepository()
	clock := fixedOfferClock{now: time.Now()}
	useCase := NewAddOperatorUseCase(relationships, activity, clock, &sequentialAggregatorIDs{})

	rel, err := useCase.Execute(context.Background(), AddOperatorCommand{
		AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, FeeRatePct: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.Status != domain.RelationshipStatusPending {
		t.Fatalf("expected pending status until the operator accepts, got %q", rel.Status)
	}
	if len(activity.entries) != 0 {
		t.Fatalf("expected no activity entry before acceptance, got %d", len(activity.entries))
	}
}

func TestAddOperatorUseCase_InviteDoesNotRecordActivityYet(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	activity := newFakeActivityRepository()
	useCase := NewAddOperatorUseCase(relationships, activity, fixedOfferClock{now: time.Now()}, &sequentialAggregatorIDs{})

	rel, err := useCase.Execute(context.Background(), AddOperatorCommand{
		AggregatorTenantID: "aggregator-1", InvitedEmail: "new@example.com",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.Status != domain.RelationshipStatusPending {
		t.Fatalf("expected pending, got %q", rel.Status)
	}
	if len(activity.entries) != 0 {
		t.Fatalf("expected no activity entry for a pending invite, got %d", len(activity.entries))
	}
}

func TestAddOperatorUseCase_RefusesDuplicateActiveRelationship(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	useCase := NewAddOperatorUseCase(relationships, newFakeActivityRepository(), fixedOfferClock{now: time.Now()}, &sequentialAggregatorIDs{})

	cmd := AddOperatorCommand{
		AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator},
	}
	if _, err := useCase.Execute(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error on first add: %v", err)
	}
	if _, err := useCase.Execute(context.Background(), cmd); err != ErrRelationshipAlreadyExists {
		t.Fatalf("expected ErrRelationshipAlreadyExists, got %v", err)
	}
}

func TestAddOperatorUseCase_RequiresOperatorOrInvite(t *testing.T) {
	useCase := NewAddOperatorUseCase(newFakeRelationshipRepository(), newFakeActivityRepository(), fixedOfferClock{now: time.Now()}, &sequentialAggregatorIDs{})
	_, err := useCase.Execute(context.Background(), AddOperatorCommand{
		AggregatorTenantID: "aggregator-1", Types: []domain.RelationshipType{domain.RelationshipOwnerOperator},
	})
	if err != ErrOperatorIdentifierRequired {
		t.Fatalf("expected ErrOperatorIdentifierRequired, got %v", err)
	}
}

func TestAcceptAggregatorInviteUseCase_RequiresMatchingEmail(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	now := time.Now()
	rel, err := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", InvitedEmail: "invited@example.com",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected fixture error: %v", err)
	}
	_, _ = relationships.Create(context.Background(), rel)

	useCase := NewAcceptAggregatorInviteUseCase(relationships, newFakeActivityRepository(), fixedOfferClock{now: now}, &sequentialAggregatorIDs{})

	_, err = useCase.Execute(context.Background(), AcceptAggregatorInviteCommand{
		RelationshipID: "rel-1", AcceptingTenantID: "operator-9", AcceptingAccountEmail: "wrong@example.com",
	})
	if err != ErrInvitedEmailMismatch {
		t.Fatalf("expected ErrInvitedEmailMismatch, got %v", err)
	}

	accepted, err := useCase.Execute(context.Background(), AcceptAggregatorInviteCommand{
		RelationshipID: "rel-1", AcceptingTenantID: "operator-9", AcceptingAccountEmail: "Invited@Example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error with case-insensitive match: %v", err)
	}
	if accepted.Status != domain.RelationshipStatusActive {
		t.Fatalf("expected active status, got %q", accepted.Status)
	}
}

func TestAcceptAggregatorInviteUseCase_DirectLinkOnlyByTheNamedOperator(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	activity := newFakeActivityRepository()
	now := time.Now()
	rel, err := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected fixture error: %v", err)
	}
	_, _ = relationships.Create(context.Background(), rel)

	useCase := NewAcceptAggregatorInviteUseCase(relationships, activity, fixedOfferClock{now: now}, &sequentialAggregatorIDs{})

	for _, tenant := range []string{"operator-9", "aggregator-1", ""} {
		if _, err := useCase.Execute(context.Background(), AcceptAggregatorInviteCommand{
			RelationshipID: "rel-1", AcceptingTenantID: tenant,
		}); err != ErrRelationshipNotFound {
			t.Fatalf("tenant %q: expected ErrRelationshipNotFound, got %v", tenant, err)
		}
	}
	if stored, _, _ := relationships.FindByID(context.Background(), "rel-1"); stored.Status != domain.RelationshipStatusPending {
		t.Fatalf("expected link to stay pending after refused accepts, got %q", stored.Status)
	}

	accepted, err := useCase.Execute(context.Background(), AcceptAggregatorInviteCommand{
		RelationshipID: "rel-1", AcceptingTenantID: "operator-1",
	})
	if err != nil {
		t.Fatalf("unexpected error accepting as the named operator: %v", err)
	}
	if accepted.Status != domain.RelationshipStatusActive || accepted.OperatorTenantID != "operator-1" {
		t.Fatalf("expected active link to operator-1, got %+v", accepted)
	}
	if len(activity.entries) != 1 {
		t.Fatalf("expected one onboarding activity entry, got %d", len(activity.entries))
	}
}

func TestRemoveOperatorUseCase_RefusesWhileOperatorHasActiveJobs(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	offers := newFakeOfferRepository(newLifecycleRepository())
	jobs := newLifecycleRepository()
	now := time.Now()

	rel, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	_, _ = relationships.Create(context.Background(), rel)

	job, _ := domain.NewJob(domain.Job{
		ID: "job-1", TenantID: "operator-1",
		Pickup: domain.JobStop{Address: "A"}, Dropoff: domain.JobStop{Address: "B"},
		Status: domain.StatusAssigned, CreatedAt: now, UpdatedAt: now,
	})
	_ = jobs.Save(job)

	acceptedOffer, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 1000, Status: domain.OfferStatusAccepted, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	_ = offers.Save(context.Background(), acceptedOffer)

	useCase := NewRemoveOperatorUseCase(relationships, offers, jobs, fixedOfferClock{now: now})
	_, err := useCase.Execute(context.Background(), RemoveOperatorCommand{
		AggregatorTenantID: "aggregator-1", RelationshipID: "rel-1", Reason: "test",
	})
	if err != ErrOperatorHasActiveJobs {
		t.Fatalf("expected ErrOperatorHasActiveJobs, got %v", err)
	}
}

func TestRemoveOperatorUseCase_SucceedsWhenNoActiveJobs(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	offers := newFakeOfferRepository(newLifecycleRepository())
	jobs := newLifecycleRepository()
	now := time.Now()

	rel, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	_, _ = relationships.Create(context.Background(), rel)

	useCase := NewRemoveOperatorUseCase(relationships, offers, jobs, fixedOfferClock{now: now})
	ended, err := useCase.Execute(context.Background(), RemoveOperatorCommand{
		AggregatorTenantID: "aggregator-1", RelationshipID: "rel-1", Reason: "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ended.Status != domain.RelationshipStatusEnded {
		t.Fatalf("expected ended status, got %q", ended.Status)
	}
}

func TestRemoveOperatorUseCase_RefusesWrongAggregator(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	now := time.Now()
	rel, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	_, _ = relationships.Create(context.Background(), rel)

	useCase := NewRemoveOperatorUseCase(relationships, newFakeOfferRepository(newLifecycleRepository()), newLifecycleRepository(), fixedOfferClock{now: now})
	_, err := useCase.Execute(context.Background(), RemoveOperatorCommand{
		AggregatorTenantID: "someone-else", RelationshipID: "rel-1",
	})
	if err != ErrRelationshipNotYours {
		t.Fatalf("expected ErrRelationshipNotYours, got %v", err)
	}
}

func TestListLinkedOperatorsUseCase_OnlyReturnsActiveRelationships(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	now := time.Now()

	active, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-active", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1", Status: domain.RelationshipStatusActive,
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	_, _ = relationships.Create(context.Background(), active)

	pending, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-pending", AggregatorTenantID: "aggregator-1", InvitedEmail: "x@example.com",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, CreatedAt: now, UpdatedAt: now,
	})
	_, _ = relationships.Create(context.Background(), pending)

	useCase := NewListLinkedOperatorsUseCase(relationships, newLifecycleRepository(), fakeFleetCounts{vehicles: 5, drivers: 3}, newFakeRatingRepository())
	operators, err := useCase.Execute(context.Background(), "aggregator-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(operators) != 1 {
		t.Fatalf("expected only the active relationship, got %d", len(operators))
	}
	if operators[0].VehicleCount != 5 || operators[0].DriverCount != 3 {
		t.Fatalf("expected fleet counts to be populated, got %+v", operators[0])
	}
}
