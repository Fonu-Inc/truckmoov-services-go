package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// fakeOfferRepository is a minimal, non-concurrent-safe stand-in. It exists to
// test the use cases' own logic — capacity gating, ownership checks, error
// mapping — not repository atomicity, which the infrastructure package's own
// tests already cover against the real implementation.
type fakeOfferRepository struct {
	offers map[string]domain.JobOffer
	jobs   JobRepository
}

func newFakeOfferRepository(jobs JobRepository) *fakeOfferRepository {
	return &fakeOfferRepository{offers: map[string]domain.JobOffer{}, jobs: jobs}
}

func (r *fakeOfferRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakeOfferRepository) Create(_ context.Context, offer domain.JobOffer) (domain.JobOffer, error) {
	r.offers[offer.ID] = offer
	return offer, nil
}

func (r *fakeOfferRepository) FindByID(_ context.Context, id string) (domain.JobOffer, bool, error) {
	offer, ok := r.offers[id]
	return offer, ok, nil
}

func (r *fakeOfferRepository) List(_ context.Context, query JobOfferQuery) ([]domain.JobOffer, int, error) {
	out := []domain.JobOffer{}
	for _, offer := range r.offers {
		tenant := offer.TargetTenantID
		if query.Role == "offering" {
			tenant = offer.OfferingTenantID
		}
		if tenant == query.TenantID {
			out = append(out, offer)
		}
	}
	return out, len(out), nil
}

func (r *fakeOfferRepository) CountPending(_ context.Context, targetTenantID string) (int, error) {
	count := 0
	for _, offer := range r.offers {
		if offer.TargetTenantID == targetTenantID && offer.Status == domain.OfferStatusPending {
			count++
		}
	}
	return count, nil
}

func (r *fakeOfferRepository) ListAccepted(_ context.Context, offeringTenantID, targetTenantID string) ([]domain.JobOffer, error) {
	matched := []domain.JobOffer{}
	for _, offer := range r.offers {
		if offer.OfferingTenantID == offeringTenantID && offer.TargetTenantID == targetTenantID && offer.Status == domain.OfferStatusAccepted {
			matched = append(matched, offer)
		}
	}
	return matched, nil
}

func (r *fakeOfferRepository) FindExpiring(_ context.Context, before time.Time, limit int) ([]domain.JobOffer, error) {
	due := []domain.JobOffer{}
	for _, offer := range r.offers {
		if offer.Status == domain.OfferStatusPending && !offer.ExpiresAt.After(before) {
			due = append(due, offer)
		}
	}
	return due, nil
}

func (r *fakeOfferRepository) SaveExpired(ctx context.Context, offer domain.JobOffer) error {
	return r.Save(ctx, offer)
}

func (r *fakeOfferRepository) Save(_ context.Context, offer domain.JobOffer) error {
	r.offers[offer.ID] = offer
	return nil
}

func (r *fakeOfferRepository) Accept(
	_ context.Context,
	offerID, targetTenantID, note string,
	now time.Time,
) (AcceptedOffer, error) {
	offer, ok := r.offers[offerID]
	if !ok {
		return AcceptedOffer{}, ErrJobOfferNotFound
	}
	if offer.TargetTenantID != targetTenantID {
		return AcceptedOffer{}, ErrNotYourOffer
	}
	accepted, err := offer.Accept(note, now)
	if err != nil {
		return AcceptedOffer{}, err
	}
	r.offers[accepted.ID] = accepted

	job, found := r.jobs.FindByTenantAndID(offer.OfferingTenantID, offer.JobID)
	if !found {
		return AcceptedOffer{}, ErrJobNotFound
	}
	transferred, err := job.TransferToCarrier(targetTenantID, now, "t")
	if err != nil {
		return AcceptedOffer{}, err
	}
	_ = r.jobs.Delete(offer.OfferingTenantID, offer.JobID)
	_ = r.jobs.Save(transferred)
	return AcceptedOffer{Offer: accepted, Job: transferred}, nil
}

type fixedEntitlementsReader struct {
	max *int
}

func (r fixedEntitlementsReader) MaxActiveJobs(context.Context, string) (*int, error) {
	return r.max, nil
}

func intPtr(v int) *int { return &v }

type recordingOfferPublisher struct {
	created   []domain.JobOffer
	responded []domain.JobOffer
}

func (p *recordingOfferPublisher) PublishOfferCreated(_ context.Context, offer domain.JobOffer) error {
	p.created = append(p.created, offer)
	return nil
}

func (p *recordingOfferPublisher) PublishOfferResponded(_ context.Context, offer domain.JobOffer) error {
	p.responded = append(p.responded, offer)
	return nil
}

type fixedOfferClock struct{ now time.Time }

func (c fixedOfferClock) Now() time.Time { return c.now }

type sequentialOfferIDs struct{ n int }

func (g *sequentialOfferIDs) New() string {
	g.n++
	return "offer-" + string(rune('0'+g.n))
}

func TestCreateJobOfferRefusesAnOfferForAJobTheTenantDoesNotOwn(t *testing.T) {
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	clock := fixedOfferClock{now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	useCase := NewCreateJobOfferUseCase(offers, jobs, &recordingOfferPublisher{}, clock, &sequentialOfferIDs{})

	_, err := useCase.Execute(context.Background(), CreateJobOfferCommand{
		OfferingTenantID: "tenant-owner", JobID: "job-does-not-exist", TargetTenantID: "tenant-carrier",
		OfferedRateNgn: 100000, ExpiresAt: clock.now.Add(time.Hour),
	})
	if err != ErrJobNotFound {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}

func mustSaveJob(t *testing.T, repo *lifecycleRepository, tenantID, id string) domain.Job {
	t.Helper()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID: id, TenantID: tenantID,
		Pickup: domain.JobStop{Address: "Apapa Port"}, Dropoff: domain.JobStop{Address: "Lekki"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("build job: %v", err)
	}
	if err := repo.Save(job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	return job
}

func TestCreateJobOfferPublishesToTheTargetTenant(t *testing.T) {
	jobs := newLifecycleRepository()
	mustSaveJob(t, jobs, "tenant-owner", "job-1")
	offers := newFakeOfferRepository(jobs)
	publisher := &recordingOfferPublisher{}
	clock := fixedOfferClock{now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	useCase := NewCreateJobOfferUseCase(offers, jobs, publisher, clock, &sequentialOfferIDs{})

	offer, err := useCase.Execute(context.Background(), CreateJobOfferCommand{
		OfferingTenantID: "tenant-owner", JobID: "job-1", TargetTenantID: "tenant-carrier",
		OfferedRateNgn: 100000, ExpiresAt: clock.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Offers are time-critical; a carrier who is looking at the app should see
	// this without refreshing.
	if len(publisher.created) != 1 || publisher.created[0].ID != offer.ID {
		t.Fatal("expected the offer to be published on creation")
	}
}

func TestAcceptJobOfferRefusesOverTheActiveJobCap(t *testing.T) {
	jobs := newLifecycleRepository()
	mustSaveJob(t, jobs, "tenant-owner", "job-1")
	// The carrier already has two active jobs of its own.
	mustSaveJob(t, jobs, "tenant-carrier", "existing-1")
	mustSaveJob(t, jobs, "tenant-carrier", "existing-2")

	offers := newFakeOfferRepository(jobs)
	clock := fixedOfferClock{now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	offer, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "tenant-owner", TargetTenantID: "tenant-carrier",
		OfferedRateNgn: 100000, ExpiresAt: clock.now.Add(time.Hour), CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	_, _ = offers.Create(context.Background(), offer)

	activeJobs := func(_ context.Context, tenantID string) (int, error) {
		count := 0
		for _, job := range jobs.FindByTenant(tenantID) {
			if job.IsActive() {
				count++
			}
		}
		return count, nil
	}
	// Capped at 2, and the carrier is already at 2.
	useCase := NewAcceptJobOfferUseCase(offers, fixedEntitlementsReader{max: intPtr(2)}, activeJobs, &recordingOfferPublisher{}, clock)

	_, err := useCase.Execute(context.Background(), RespondToJobOfferCommand{OfferID: "offer-1", TenantID: "tenant-carrier"})
	if err != ErrOverActiveJobCap {
		t.Fatalf("expected ErrOverActiveJobCap, got %v", err)
	}

	// Refused before anything changed: the offer is still pending and the job
	// still belongs to the offering tenant.
	stillPending, _, _ := offers.FindByID(context.Background(), "offer-1")
	if stillPending.Status != domain.OfferStatusPending {
		t.Fatalf("expected the offer to remain pending, got %s", stillPending.Status)
	}
	if _, found := jobs.FindByTenantAndID("tenant-carrier", "job-1"); found {
		t.Fatal("the job must not transfer when the accept was refused")
	}
}

func TestAcceptJobOfferAllowsExactlyUpToTheCap(t *testing.T) {
	jobs := newLifecycleRepository()
	mustSaveJob(t, jobs, "tenant-owner", "job-1")
	mustSaveJob(t, jobs, "tenant-carrier", "existing-1")

	offers := newFakeOfferRepository(jobs)
	clock := fixedOfferClock{now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	offer, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "tenant-owner", TargetTenantID: "tenant-carrier",
		OfferedRateNgn: 100000, ExpiresAt: clock.now.Add(time.Hour), CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	_, _ = offers.Create(context.Background(), offer)

	activeJobs := func(_ context.Context, tenantID string) (int, error) {
		count := 0
		for _, job := range jobs.FindByTenant(tenantID) {
			if job.IsActive() {
				count++
			}
		}
		return count, nil
	}
	// One active job, cap of two: accepting brings it to two, which fits.
	useCase := NewAcceptJobOfferUseCase(offers, fixedEntitlementsReader{max: intPtr(2)}, activeJobs, &recordingOfferPublisher{}, clock)

	result, err := useCase.Execute(context.Background(), RespondToJobOfferCommand{OfferID: "offer-1", TenantID: "tenant-carrier"})
	if err != nil {
		t.Fatalf("expected acceptance at exactly the cap to succeed: %v", err)
	}
	if result.Job.TenantID != "tenant-carrier" {
		t.Fatal("expected the job to transfer")
	}
}

func TestAcceptJobOfferTreatsAnUnsetCapAsUnlimited(t *testing.T) {
	jobs := newLifecycleRepository()
	mustSaveJob(t, jobs, "tenant-owner", "job-1")
	for i := 0; i < 50; i++ {
		mustSaveJob(t, jobs, "tenant-carrier", "existing-"+string(rune('a'+i)))
	}

	offers := newFakeOfferRepository(jobs)
	clock := fixedOfferClock{now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	offer, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "tenant-owner", TargetTenantID: "tenant-carrier",
		OfferedRateNgn: 100000, ExpiresAt: clock.now.Add(time.Hour), CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	_, _ = offers.Create(context.Background(), offer)

	// nil means no cap — the same "unset is unlimited, not zero" rule as every
	// other quota on the platform.
	useCase := NewAcceptJobOfferUseCase(offers, fixedEntitlementsReader{max: nil}, nil, &recordingOfferPublisher{}, clock)

	if _, err := useCase.Execute(context.Background(), RespondToJobOfferCommand{OfferID: "offer-1", TenantID: "tenant-carrier"}); err != nil {
		t.Fatalf("expected no cap to allow the accept, got %v", err)
	}
}

func TestAcceptJobOfferRefusesTheWrongTenant(t *testing.T) {
	jobs := newLifecycleRepository()
	mustSaveJob(t, jobs, "tenant-owner", "job-1")
	offers := newFakeOfferRepository(jobs)
	clock := fixedOfferClock{now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	offer, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "offer-1", JobID: "job-1", OfferingTenantID: "tenant-owner", TargetTenantID: "tenant-carrier",
		OfferedRateNgn: 100000, ExpiresAt: clock.now.Add(time.Hour), CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	_, _ = offers.Create(context.Background(), offer)

	useCase := NewAcceptJobOfferUseCase(offers, nil, nil, &recordingOfferPublisher{}, clock)
	_, err := useCase.Execute(context.Background(), RespondToJobOfferCommand{OfferID: "offer-1", TenantID: "tenant-imposter"})
	if err != ErrNotYourOffer {
		t.Fatalf("expected ErrNotYourOffer, got %v", err)
	}
}

func TestActiveJobCapacityReportsAServerDecidedReason(t *testing.T) {
	activeJobs := func(context.Context, string) (int, error) { return 3, nil }
	useCase := NewActiveJobCapacityUseCase(fixedEntitlementsReader{max: intPtr(3)}, activeJobs)

	capacity, err := useCase.Execute(context.Background(), "tenant-carrier")
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if capacity.Eligible {
		t.Fatal("expected ineligible at the cap")
	}
	if capacity.Reason == "" {
		t.Fatal("expected a human-readable reason, decided server-side")
	}
	if capacity.Current != 3 || capacity.Max == nil || *capacity.Max != 3 {
		t.Fatalf("expected current and max both reported, got %+v", capacity)
	}
}

func TestExpireJobOffersOnlyClosesOffersPastTheirWindow(t *testing.T) {
	jobs := newLifecycleRepository()
	offers := newFakeOfferRepository(jobs)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	expired, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "expired", JobID: "j1", OfferingTenantID: "a", TargetTenantID: "b",
		OfferedRateNgn: 1000, ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-49 * time.Hour), UpdatedAt: now.Add(-49 * time.Hour),
	})
	stillOpen, _ := domain.NewJobOffer(domain.JobOffer{
		ID: "still-open", JobID: "j2", OfferingTenantID: "a", TargetTenantID: "b",
		OfferedRateNgn: 1000, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})
	_, _ = offers.Create(context.Background(), expired)
	_, _ = offers.Create(context.Background(), stillOpen)

	useCase := NewExpireJobOffersUseCase(offers, fixedOfferClock{now: now})
	count, err := useCase.Execute(context.Background())
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 offer expired, got %d", count)
	}

	closed, _, _ := offers.FindByID(context.Background(), "expired")
	if closed.Status != domain.OfferStatusExpired {
		t.Fatalf("expected the offer to move to expired, got %s", closed.Status)
	}
	open, _, _ := offers.FindByID(context.Background(), "still-open")
	if open.Status != domain.OfferStatusPending {
		t.Fatal("an offer still within its window must not be touched by the sweep")
	}
}
