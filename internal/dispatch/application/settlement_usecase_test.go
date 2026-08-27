package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func TestPreviousWeekWindow_MondayToMonday(t *testing.T) {
	// Wednesday 2026-03-04 (a Wednesday) -> previous week is Mon 2026-02-23 to Mon 2026-03-02.
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	start, end := previousWeekWindow(now)

	wantStart := time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Fatalf("expected start %v, got %v", wantStart, start)
	}
	if !end.Equal(wantEnd) {
		t.Fatalf("expected end %v, got %v", wantEnd, end)
	}
}

func TestPreviousWeekWindow_HandlesSunday(t *testing.T) {
	// Sunday 2026-03-08 belongs to the week starting Mon 2026-03-02; previous
	// week is Mon 2026-02-23 to Mon 2026-03-02.
	now := time.Date(2026, 3, 8, 23, 0, 0, 0, time.UTC)
	start, end := previousWeekWindow(now)
	if start.Weekday() != time.Monday || end.Weekday() != time.Monday {
		t.Fatalf("expected both bounds to fall on Monday, got %v / %v", start.Weekday(), end.Weekday())
	}
	if !end.Before(now) {
		t.Fatalf("expected end before now")
	}
}

func deliveredJobWithTimestamp(id, tenantID string, amount float64, deliveredAt time.Time) domain.Job {
	job, _ := domain.NewJob(domain.Job{
		ID: id, TenantID: tenantID,
		Pickup: domain.JobStop{Address: "Lagos"}, Dropoff: domain.JobStop{Address: "Ibadan"},
		Status: domain.StatusDelivered, AmountNgn: amount,
		CreatedAt: deliveredAt.Add(-time.Hour), UpdatedAt: deliveredAt,
	})
	job.Timeline = []domain.JobTimelineEntry{
		{ID: "t1", Type: "job_status_updated", OccurredAt: deliveredAt, Metadata: map[string]string{"to_status": "delivered"}},
	}
	return job
}

func TestWeeklySettlementCloseUseCase_ClosesAPeriodWithDeliveredJobs(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	settlements := newFakeSettlementRepository()

	// now is a Wednesday; previous week is [Mon-7d, Mon).
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	periodStart, _ := previousWeekWindow(now)
	deliveredAt := periodStart.Add(24 * time.Hour)

	rel, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, FeeRatePct: 10,
		CreatedAt: periodStart.Add(-time.Hour), UpdatedAt: periodStart.Add(-time.Hour),
	})
	_, _ = relationships.Create(context.Background(), rel)

	job := deliveredJobWithTimestamp("job-1", "operator-1", 10000, deliveredAt)
	_ = jobs.Save(job)
	accepted, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 9000, Status: domain.OfferStatusAccepted, ExpiresAt: deliveredAt.Add(time.Hour),
		CreatedAt: deliveredAt.Add(-2 * time.Hour), UpdatedAt: deliveredAt,
	})
	_ = offers.Save(context.Background(), accepted)

	useCase := NewWeeklySettlementCloseUseCase(relationships, offers, jobs, settlements, fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	closed, err := useCase.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if closed != 1 {
		t.Fatalf("expected 1 settlement closed, got %d", closed)
	}

	result, _, err := settlements.ListByAggregator(context.Background(), "aggregator-1", 10, 0)
	if err != nil {
		t.Fatalf("unexpected error listing: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 settlement, got %d", len(result))
	}
	if result[0].GrossEarningsNgn != 9000 {
		t.Fatalf("expected gross 9000 (the offered rate, not job amount), got %v", result[0].GrossEarningsNgn)
	}
	if result[0].AggregatorFeeNgn != 900 {
		t.Fatalf("expected fee 900, got %v", result[0].AggregatorFeeNgn)
	}
}

func TestWeeklySettlementCloseUseCase_IsIdempotent(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	settlements := newFakeSettlementRepository()

	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	periodStart, _ := previousWeekWindow(now)
	deliveredAt := periodStart.Add(24 * time.Hour)

	rel, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, FeeRatePct: 10,
		CreatedAt: periodStart.Add(-time.Hour), UpdatedAt: periodStart.Add(-time.Hour),
	})
	_, _ = relationships.Create(context.Background(), rel)

	job := deliveredJobWithTimestamp("job-1", "operator-1", 10000, deliveredAt)
	_ = jobs.Save(job)
	accepted, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "aggregator-1", TargetTenantID: "operator-1",
		OfferedRateNgn: 9000, Status: domain.OfferStatusAccepted, ExpiresAt: deliveredAt.Add(time.Hour),
		CreatedAt: deliveredAt.Add(-2 * time.Hour), UpdatedAt: deliveredAt,
	})
	_ = offers.Save(context.Background(), accepted)

	useCase := NewWeeklySettlementCloseUseCase(relationships, offers, jobs, settlements, fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	if _, err := useCase.Execute(context.Background()); err != nil {
		t.Fatalf("unexpected error on first close: %v", err)
	}
	closedAgain, err := useCase.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error on second close: %v", err)
	}
	if closedAgain != 0 {
		t.Fatalf("expected second run to close nothing (already closed), got %d", closedAgain)
	}
}

func TestWeeklySettlementCloseUseCase_SkipsZeroGrossPeriods(t *testing.T) {
	relationships := newFakeRelationshipRepository()
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	settlements := newFakeSettlementRepository()

	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	periodStart, _ := previousWeekWindow(now)

	rel, _ := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []domain.RelationshipType{domain.RelationshipOwnerOperator}, FeeRatePct: 10,
		CreatedAt: periodStart.Add(-time.Hour), UpdatedAt: periodStart.Add(-time.Hour),
	})
	_, _ = relationships.Create(context.Background(), rel)

	useCase := NewWeeklySettlementCloseUseCase(relationships, offers, jobs, settlements, fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	closed, err := useCase.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if closed != 0 {
		t.Fatalf("expected 0 settlements for a period with no delivered jobs, got %d", closed)
	}
}

func TestPaySettlementUseCase_MarksPaidAndRecordsActivity(t *testing.T) {
	settlements := newFakeSettlementRepository()
	activity := newFakeActivityRepository()
	now := time.Now()

	settlement, _ := domain.NewSettlement(domain.Settlement{
		ID: "settlement-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		RelationshipID: "rel-1", PeriodStart: now.AddDate(0, 0, -7), PeriodEnd: now,
		GrossEarningsNgn: 10000, FeeRatePctApplied: 10, CreatedAt: now, UpdatedAt: now,
	})
	_, _ = settlements.Create(context.Background(), settlement)

	useCase := NewPaySettlementUseCase(settlements, activity, fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	paid, err := useCase.Execute(context.Background(), PaySettlementCommand{
		AggregatorTenantID: "aggregator-1", SettlementID: "settlement-1", PaymentReference: "REF-1", PaidBy: "admin-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paid.Status != domain.SettlementStatusPaid {
		t.Fatalf("expected paid status, got %q", paid.Status)
	}
	if len(activity.entries) != 1 || activity.entries[0].Type != domain.ActivitySettlementPaid {
		t.Fatalf("expected one settlement_paid activity entry, got %+v", activity.entries)
	}
}

func TestPaySettlementUseCase_RefusesWrongAggregator(t *testing.T) {
	settlements := newFakeSettlementRepository()
	now := time.Now()
	settlement, _ := domain.NewSettlement(domain.Settlement{
		ID: "settlement-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		RelationshipID: "rel-1", PeriodStart: now.AddDate(0, 0, -7), PeriodEnd: now,
		GrossEarningsNgn: 10000, FeeRatePctApplied: 10, CreatedAt: now, UpdatedAt: now,
	})
	_, _ = settlements.Create(context.Background(), settlement)

	useCase := NewPaySettlementUseCase(settlements, newFakeActivityRepository(), fixedOfferClock{now: now}, &sequentialAggregatorIDs{})
	_, err := useCase.Execute(context.Background(), PaySettlementCommand{
		AggregatorTenantID: "someone-else", SettlementID: "settlement-1", PaymentReference: "REF-1",
	})
	if err != ErrSettlementNotYours {
		t.Fatalf("expected ErrSettlementNotYours, got %v", err)
	}
}
