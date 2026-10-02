package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func newTestOffer(t *testing.T, id, jobID, offeringTenant, targetTenant string, expiresAt time.Time) domain.JobOffer {
	t.Helper()
	offer, err := domain.NewJobOffer(domain.JobOffer{
		ID: id, JobID: jobID, OfferingTenantID: offeringTenant, TargetTenantID: targetTenant,
		OfferedRateNgn: 150000, ExpiresAt: expiresAt,
		CreatedAt: expiresAt.Add(-48 * time.Hour), UpdatedAt: expiresAt.Add(-48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("fixture: build offer: %v", err)
	}
	return offer
}

func TestInMemoryJobOfferRepository_AcceptTransfersTheJobToTheCarrier(t *testing.T) {
	ctx := context.Background()
	jobs := NewInMemoryJobRepository()
	job := newTestJob(t, "tenant-owner", "job-1")
	if err := jobs.Save(job); err != nil {
		t.Fatalf("save job: %v", err)
	}

	offers := NewInMemoryJobOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	offer := newTestOffer(t, "offer-1", "job-1", "tenant-owner", "tenant-carrier", now.Add(time.Hour))
	if _, err := offers.Create(ctx, offer); err != nil {
		t.Fatalf("create offer: %v", err)
	}

	result, err := offers.Accept(ctx, "offer-1", "tenant-carrier", "accepted, thanks", now)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if result.Offer.Status != domain.OfferStatusAccepted {
		t.Fatalf("expected accepted, got %s", result.Offer.Status)
	}
	if result.Job.TenantID != "tenant-carrier" {
		t.Fatalf("expected the job to belong to the carrier, got %s", result.Job.TenantID)
	}

	// The old tenant no longer holds it, and the new tenant does — this is the
	// "accepted but unassigned" failure the requirements name, checked from
	// both directions.
	if _, err := jobs.FindByTenantAndID("tenant-owner", "job-1"); err == nil {
		t.Fatal("the offering tenant should no longer hold the job after acceptance")
	}
	transferredJob, err := jobs.FindByTenantAndID("tenant-carrier", "job-1")
	if err != nil {
		t.Fatal("expected the carrier tenant to hold the job after acceptance")
	}
	if transferredJob.Status == domain.StatusCancelled {
		t.Fatal("the job's own workflow status should be untouched by the transfer")
	}
}

func TestInMemoryJobOfferRepository_FirstAcceptanceWithdrawsTheRest(t *testing.T) {
	ctx := context.Background()
	jobs := NewInMemoryJobRepository()
	if err := jobs.Save(newTestJob(t, "tenant-owner", "job-1")); err != nil {
		t.Fatalf("save job: %v", err)
	}

	offers := NewInMemoryJobOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for _, carrier := range []string{"tenant-a", "tenant-b", "tenant-c"} {
		offer := newTestOffer(t, "offer-"+carrier, "job-1", "tenant-owner", carrier, now.Add(time.Hour))
		if _, err := offers.Create(ctx, offer); err != nil {
			t.Fatalf("create offer for %s: %v", carrier, err)
		}
	}

	if _, err := offers.Accept(ctx, "offer-tenant-b", "tenant-b", "", now); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Two carriers must not both believe they hold the job.
	a, _, _ := offers.FindByID(ctx, "offer-tenant-a")
	if a.Status != domain.OfferStatusWithdrawn {
		t.Fatalf("expected the sibling offer to be withdrawn, got %s", a.Status)
	}
	c, _, _ := offers.FindByID(ctx, "offer-tenant-c")
	if c.Status != domain.OfferStatusWithdrawn {
		t.Fatalf("expected the sibling offer to be withdrawn, got %s", c.Status)
	}
	b, _, _ := offers.FindByID(ctx, "offer-tenant-b")
	if b.Status != domain.OfferStatusAccepted {
		t.Fatalf("expected the accepted offer to stay accepted, got %s", b.Status)
	}
}

func TestInMemoryJobOfferRepository_AcceptRefusesTheWrongTenant(t *testing.T) {
	ctx := context.Background()
	jobs := NewInMemoryJobRepository()
	_ = jobs.Save(newTestJob(t, "tenant-owner", "job-1"))
	offers := NewInMemoryJobOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	_, _ = offers.Create(ctx, newTestOffer(t, "offer-1", "job-1", "tenant-owner", "tenant-carrier", now.Add(time.Hour)))

	// A carrier the offer was not made to must not be able to accept it by
	// guessing an offer id.
	if _, err := offers.Accept(ctx, "offer-1", "tenant-imposter", "", now); err != application.ErrNotYourOffer {
		t.Fatalf("expected ErrNotYourOffer, got %v", err)
	}
}

func TestInMemoryJobOfferRepository_AcceptRefusesAnExpiredOffer(t *testing.T) {
	ctx := context.Background()
	jobs := NewInMemoryJobRepository()
	_ = jobs.Save(newTestJob(t, "tenant-owner", "job-1"))
	offers := NewInMemoryJobOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	_, _ = offers.Create(ctx, newTestOffer(t, "offer-1", "job-1", "tenant-owner", "tenant-carrier", now.Add(-time.Minute)))

	if _, err := offers.Accept(ctx, "offer-1", "tenant-carrier", "", now); err == nil {
		t.Fatal("expected accepting an expired offer to be refused")
	}
	// Rejected, not silently left pending.
	stillThere, _, _ := offers.FindByID(ctx, "offer-1")
	if stillThere.Status != domain.OfferStatusPending {
		t.Fatalf("a refused accept must not change the offer's status, got %s", stillThere.Status)
	}
}

func TestInMemoryJobOfferRepository_FindExpiringOnlyReturnsPendingPastTheirWindow(t *testing.T) {
	ctx := context.Background()
	jobs := NewInMemoryJobRepository()
	offers := NewInMemoryJobOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	_, _ = offers.Create(ctx, newTestOffer(t, "expired-pending", "job-1", "a", "b", now.Add(-time.Hour)))
	_, _ = offers.Create(ctx, newTestOffer(t, "not-expired", "job-2", "a", "b", now.Add(time.Hour)))
	expiredButAccepted := newTestOffer(t, "expired-accepted", "job-3", "a", "b", now.Add(-time.Hour))
	expiredButAccepted.Status = domain.OfferStatusAccepted
	_, _ = offers.Create(ctx, expiredButAccepted)

	due, err := offers.FindExpiring(ctx, now, 10)
	if err != nil {
		t.Fatalf("find expiring: %v", err)
	}
	if len(due) != 1 || due[0].ID != "expired-pending" {
		t.Fatalf("expected only the expired pending offer, got %+v", due)
	}
}

func TestInMemoryJobOfferRepository_CountPendingIsScopedToTheTargetTenant(t *testing.T) {
	ctx := context.Background()
	jobs := NewInMemoryJobRepository()
	offers := NewInMemoryJobOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	_, _ = offers.Create(ctx, newTestOffer(t, "o1", "job-1", "owner", "carrier-a", now.Add(time.Hour)))
	_, _ = offers.Create(ctx, newTestOffer(t, "o2", "job-2", "owner", "carrier-a", now.Add(time.Hour)))
	_, _ = offers.Create(ctx, newTestOffer(t, "o3", "job-3", "owner", "carrier-b", now.Add(time.Hour)))

	count, err := offers.CountPending(ctx, "carrier-a")
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

func TestInMemoryJobOfferRepository_AcceptKeepsBothJobsWhenCarrierHasTheSameID(t *testing.T) {
	ctx := context.Background()
	jobs := NewInMemoryJobRepository()
	if err := jobs.Save(newTestJob(t, "tenant-owner", "job-1")); err != nil {
		t.Fatalf("save owner job: %v", err)
	}
	if err := jobs.Save(newTestJob(t, "tenant-carrier", "job-1")); err != nil {
		t.Fatalf("save carrier job: %v", err)
	}
	offers := NewInMemoryJobOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	if _, err := offers.Create(ctx, newTestOffer(t, "offer-1", "job-1", "tenant-owner", "tenant-carrier", now.Add(time.Hour))); err != nil {
		t.Fatalf("create offer: %v", err)
	}

	result, err := offers.Accept(ctx, "offer-1", "tenant-carrier", "", now)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if result.Job.ID == "job-1" || result.Offer.CarrierJob() != result.Job.ID {
		t.Fatalf("expected a new carrier id linked from the offer, got job %q offer %q", result.Job.ID, result.Offer.CarrierJob())
	}
	if _, err := jobs.FindByTenantAndID("tenant-carrier", "job-1"); err != nil {
		t.Fatalf("the carrier's own job must be untouched: %v", err)
	}
	if _, err := jobs.FindByTenantAndID("tenant-carrier", result.Job.ID); err != nil {
		t.Fatalf("the transferred copy must exist under its new id: %v", err)
	}
}
