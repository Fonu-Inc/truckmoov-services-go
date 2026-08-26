package domain

import (
	"testing"
	"time"
)

func validOfferFixture(t *testing.T, now time.Time) JobOffer {
	t.Helper()
	offer, err := NewJobOffer(JobOffer{
		ID:               "offer-1",
		JobID:            "job-1",
		OfferingTenantID: "tenant-owner",
		TargetTenantID:   "tenant-carrier",
		OfferedRateNgn:   150000,
		ExpiresAt:        now.Add(48 * time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if err != nil {
		t.Fatalf("fixture: unexpected error building offer: %v", err)
	}
	return offer
}

func TestNewJobOfferRejectsAnOfferToTheOfferingTenantsOwnJobs(t *testing.T) {
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	_, err := NewJobOffer(JobOffer{
		ID: "o1", JobID: "j1", OfferingTenantID: "tenant-a", TargetTenantID: "tenant-a",
		OfferedRateNgn: 10000, ExpiresAt: now.Add(time.Hour),
	})
	if err == nil {
		t.Fatal("expected an error offering a job to the tenant that owns it")
	}
}

func TestNewJobOfferRejectsAZeroRate(t *testing.T) {
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	_, err := NewJobOffer(JobOffer{
		ID: "o1", JobID: "j1", OfferingTenantID: "a", TargetTenantID: "b",
		OfferedRateNgn: 0, ExpiresAt: now.Add(time.Hour),
	})
	if err == nil {
		t.Fatal("expected an error for a zero offered rate")
	}
}

func TestOfferIsExpiredJudgesAgainstTheGivenClockNotAStoredFlag(t *testing.T) {
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	offer := validOfferFixture(t, now)

	if offer.IsExpired(now) {
		t.Fatal("a freshly created offer is not expired")
	}
	if !offer.IsExpired(now.Add(49 * time.Hour)) {
		t.Fatal("expected the offer to read as expired once its window passed")
	}
}

func TestAcceptingAnExpiredOfferIsRefusedEvenThoughTheDataLooksPending(t *testing.T) {
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	offer := validOfferFixture(t, now)
	afterExpiry := now.Add(49 * time.Hour)

	// The requirements are explicit: a browser that is minutes old must not be
	// able to accept an offer that has since expired. The status column alone
	// still says pending; only the live clock check catches this.
	if _, err := offer.Accept("", afterExpiry); err == nil {
		t.Fatal("expected accepting an expired offer to be refused")
	}
}

func TestDeclinedOfferCannotBeAcceptedAfterwards(t *testing.T) {
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	offer := validOfferFixture(t, now)

	declined, err := offer.Decline("not for us", now)
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	if _, err := declined.Accept("", now); err == nil {
		t.Fatal("expected accepting an already-declined offer to be refused")
	}
}

func TestWithdrawOnlyTouchesAPendingOffer(t *testing.T) {
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	offer := validOfferFixture(t, now)

	declined, _ := offer.Decline("no", now)
	stillDeclined := declined.Withdraw(now)
	// A carrier's own decline is a real answer and must not be overwritten by
	// the system's "someone else got it" close-out.
	if stillDeclined.Status != OfferStatusDeclined {
		t.Fatalf("expected a declined offer to stay declined, got %s", stillDeclined.Status)
	}

	pending := validOfferFixture(t, now)
	withdrawn := pending.Withdraw(now)
	if withdrawn.Status != OfferStatusWithdrawn {
		t.Fatalf("expected withdrawn, got %s", withdrawn.Status)
	}
}

func TestExpireOnlyClosesAPendingOfferPastItsWindow(t *testing.T) {
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	offer := validOfferFixture(t, now)

	if _, err := offer.Expire(now); err == nil {
		t.Fatal("expected Expire to refuse an offer that has not passed its window")
	}

	accepted, _ := offer.Accept("", now)
	if _, err := accepted.Expire(now.Add(49 * time.Hour)); err == nil {
		t.Fatal("expected Expire to refuse an already-accepted offer")
	}
}

func TestJobTransferToCarrierMovesTenantOwnershipAndKeepsATimelineRecord(t *testing.T) {
	job := validJobFixture(t)
	now := job.UpdatedAt.Add(time.Hour)

	transferred, err := job.TransferToCarrier("tenant-carrier", now, "timeline-1")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if transferred.TenantID != "tenant-carrier" {
		t.Fatalf("expected the job to belong to the carrier tenant, got %s", transferred.TenantID)
	}
	// The original job value must be untouched — Job methods return a new
	// value rather than mutating the receiver.
	if job.TenantID == "tenant-carrier" {
		t.Fatal("expected the original job to be unchanged")
	}
	if len(transferred.Timeline) == 0 {
		t.Fatal("expected a timeline entry recording the handoff")
	}
}

func TestJobTransferToCarrierRefusesACancelledJob(t *testing.T) {
	job := validJobFixture(t)
	now := job.UpdatedAt.Add(time.Hour)
	cancelled, err := job.Cancel("no longer needed", now, "t1")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if _, err := cancelled.TransferToCarrier("tenant-carrier", now, "t2"); err == nil {
		t.Fatal("expected transferring a cancelled job to be refused")
	}
}

func TestJobIsActiveExcludesOnlyTerminalStates(t *testing.T) {
	job := validJobFixture(t)
	if !job.IsActive() {
		t.Fatal("a freshly created job counts as active")
	}

	now := job.UpdatedAt.Add(time.Hour)
	cancelled, err := job.Cancel("no longer needed", now, "t1")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.IsActive() {
		t.Fatal("a cancelled job must not count against the active job cap")
	}

	assigned, _ := job.UpdateStatus(StatusAssigned, now, "t2")
	if !assigned.IsActive() {
		t.Fatal("an assigned job is still in progress and counts as active")
	}
}
