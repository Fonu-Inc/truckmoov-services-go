package infrastructure

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// InMemoryJobOfferRepository backs the in-memory deployment. Accept is guarded
// by the same mutex as every other write so it is atomic the same way the
// Postgres implementation's transaction is — one caller's Accept runs to
// completion before another can observe a half-updated state.
type InMemoryJobOfferRepository struct {
	mu     sync.Mutex
	offers map[string]domain.JobOffer
	jobs   application.JobRepository
}

func NewInMemoryJobOfferRepository(jobs application.JobRepository) *InMemoryJobOfferRepository {
	return &InMemoryJobOfferRepository{offers: map[string]domain.JobOffer{}, jobs: jobs}
}

func (r *InMemoryJobOfferRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemoryJobOfferRepository) Create(_ context.Context, offer domain.JobOffer) (domain.JobOffer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offers[offer.ID] = offer
	return offer, nil
}

func (r *InMemoryJobOfferRepository) FindByID(_ context.Context, offerID string) (domain.JobOffer, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	offer, ok := r.offers[offerID]
	return offer, ok, nil
}

func (r *InMemoryJobOfferRepository) List(_ context.Context, query application.JobOfferQuery) ([]domain.JobOffer, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	matched := []domain.JobOffer{}
	for _, offer := range r.offers {
		tenant := offer.TargetTenantID
		if query.Role == "offering" {
			tenant = offer.OfferingTenantID
		}
		if tenant != query.TenantID {
			continue
		}
		if query.Status != "" && offer.Status != query.Status {
			continue
		}
		matched = append(matched, offer)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })

	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	page := query.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit
	if offset >= len(matched) {
		return []domain.JobOffer{}, len(matched), nil
	}
	end := offset + limit
	if end > len(matched) {
		end = len(matched)
	}
	return matched[offset:end], len(matched), nil
}

func (r *InMemoryJobOfferRepository) CountPending(_ context.Context, targetTenantID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, offer := range r.offers {
		if offer.TargetTenantID == targetTenantID && offer.Status == domain.OfferStatusPending {
			count++
		}
	}
	return count, nil
}

func (r *InMemoryJobOfferRepository) ListAccepted(_ context.Context, offeringTenantID, targetTenantID string) ([]domain.JobOffer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	matched := []domain.JobOffer{}
	for _, offer := range r.offers {
		if offer.OfferingTenantID == offeringTenantID && offer.TargetTenantID == targetTenantID && offer.Status == domain.OfferStatusAccepted {
			matched = append(matched, offer)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	return matched, nil
}

func (r *InMemoryJobOfferRepository) FindExpiring(_ context.Context, before time.Time, limit int) ([]domain.JobOffer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	due := []domain.JobOffer{}
	for _, offer := range r.offers {
		if offer.Status == domain.OfferStatusPending && !offer.ExpiresAt.After(before) {
			due = append(due, offer)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ExpiresAt.Before(due[j].ExpiresAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

func (r *InMemoryJobOfferRepository) SaveExpired(ctx context.Context, offer domain.JobOffer) error {
	return r.Save(ctx, offer)
}

func (r *InMemoryJobOfferRepository) Save(_ context.Context, offer domain.JobOffer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offers[offer.ID] = offer
	return nil
}

func (r *InMemoryJobOfferRepository) Accept(
	_ context.Context,
	offerID, targetTenantID, note string,
	now time.Time,
) (application.AcceptedOffer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	offer, ok := r.offers[offerID]
	if !ok {
		return application.AcceptedOffer{}, application.ErrJobOfferNotFound
	}
	if offer.TargetTenantID != targetTenantID {
		return application.AcceptedOffer{}, application.ErrNotYourOffer
	}

	accepted, err := offer.Accept(note, now)
	if err != nil {
		return application.AcceptedOffer{}, err
	}
	r.offers[accepted.ID] = accepted

	for id, sibling := range r.offers {
		if sibling.OfferingTenantID == offer.OfferingTenantID && sibling.JobID == offer.JobID && id != offer.ID && sibling.Status == domain.OfferStatusPending {
			r.offers[id] = sibling.Withdraw(now)
		}
	}

	job, found := r.jobs.FindByTenantAndID(offer.OfferingTenantID, offer.JobID)
	if !found {
		return application.AcceptedOffer{}, application.ErrJobNotFound
	}
	transferred, err := job.TransferToCarrier(targetTenantID, now, "timeline-"+accepted.ID)
	if err != nil {
		return application.AcceptedOffer{}, err
	}
	if err := r.jobs.Delete(offer.OfferingTenantID, offer.JobID); err != nil {
		return application.AcceptedOffer{}, err
	}
	if err := r.jobs.Save(transferred); err != nil {
		return application.AcceptedOffer{}, err
	}

	return application.AcceptedOffer{Offer: accepted, Job: transferred}, nil
}
