package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

var (
	ErrJobOfferNotFound = errors.New("job offer not found")
	ErrNotYourOffer     = errors.New("this offer does not belong to you")
	ErrOverActiveJobCap = errors.New("accepting this offer would exceed the active job limit")
)

type CreateJobOfferCommand struct {
	OfferingTenantID string
	JobID            string
	TargetTenantID   string
	OfferedRateNgn   float64
	ExpiresAt        time.Time
}

// CreateJobOfferUseCase puts a job in front of a carrier.
//
// The job stays owned by the offering tenant until an offer is accepted — an
// offer is a proposal, and a job that has merely been offered has not changed
// hands.
type CreateJobOfferUseCase struct {
	offers    JobOfferRepository
	jobs      JobRepository
	publisher JobOfferEventPublisher
	clock     Clock
	idGen     IDGenerator
}

func NewCreateJobOfferUseCase(
	offers JobOfferRepository,
	jobs JobRepository,
	publisher JobOfferEventPublisher,
	clock Clock,
	idGen IDGenerator,
) *CreateJobOfferUseCase {
	return &CreateJobOfferUseCase{offers: offers, jobs: jobs, publisher: publisher, clock: clock, idGen: idGen}
}

func (u *CreateJobOfferUseCase) Execute(ctx context.Context, command CreateJobOfferCommand) (domain.JobOffer, error) {
	job, found := u.jobs.FindByTenantAndID(command.OfferingTenantID, command.JobID)
	if !found {
		return domain.JobOffer{}, ErrJobNotFound
	}
	if !job.IsActive() {
		return domain.JobOffer{}, errors.New("a completed or cancelled job cannot be offered")
	}

	offer, err := domain.NewJobOffer(domain.JobOffer{
		ID:               "offer-" + u.idGen.New(),
		JobID:            command.JobID,
		OfferingTenantID: command.OfferingTenantID,
		TargetTenantID:   command.TargetTenantID,
		OfferedRateNgn:   command.OfferedRateNgn,
		ExpiresAt:        command.ExpiresAt,
		CreatedAt:        u.clock.Now(),
		UpdatedAt:        u.clock.Now(),
	})
	if err != nil {
		return domain.JobOffer{}, err
	}

	saved, err := u.offers.Create(ctx, offer)
	if err != nil {
		return domain.JobOffer{}, fmt.Errorf("save job offer: %w", err)
	}

	if u.publisher != nil {
		if err := u.publisher.PublishOfferCreated(ctx, saved); err != nil {
			// The offer exists and is correct either way; a missed notification
			// means the carrier finds it by opening the app instead of being
			// told first. Losing the offer over a publish failure would be the
			// worse outcome.
			_ = err
		}
	}

	return saved, nil
}

type RespondToJobOfferCommand struct {
	OfferID  string
	TenantID string
	Note     string
}

// DeclineJobOfferUseCase records that a carrier does not want the job.
type DeclineJobOfferUseCase struct {
	offers    JobOfferRepository
	publisher JobOfferEventPublisher
	clock     Clock
}

func NewDeclineJobOfferUseCase(offers JobOfferRepository, publisher JobOfferEventPublisher, clock Clock) *DeclineJobOfferUseCase {
	return &DeclineJobOfferUseCase{offers: offers, publisher: publisher, clock: clock}
}

func (u *DeclineJobOfferUseCase) Execute(ctx context.Context, command RespondToJobOfferCommand) (domain.JobOffer, error) {
	offer, found, err := u.offers.FindByID(ctx, command.OfferID)
	if err != nil {
		return domain.JobOffer{}, err
	}
	if !found {
		return domain.JobOffer{}, ErrJobOfferNotFound
	}
	if offer.TargetTenantID != command.TenantID {
		return domain.JobOffer{}, ErrNotYourOffer
	}

	declined, err := offer.Decline(command.Note, u.clock.Now())
	if err != nil {
		return domain.JobOffer{}, err
	}
	if err := u.offers.Save(ctx, declined); err != nil {
		return domain.JobOffer{}, fmt.Errorf("save declined offer: %w", err)
	}

	if u.publisher != nil {
		_ = u.publisher.PublishOfferResponded(ctx, declined)
	}
	return declined, nil
}

// AcceptJobOfferUseCase is the consequential half of this screen: accepting
// does not just change a status, it hands the job to the carrier, and the
// requirements are explicit that this must happen as one unit of work.
type AcceptJobOfferUseCase struct {
	offers       JobOfferRepository
	entitlements ActiveJobEntitlementsReader
	activeJobs   func(ctx context.Context, tenantID string) (int, error)
	publisher    JobOfferEventPublisher
	clock        Clock
}

func NewAcceptJobOfferUseCase(
	offers JobOfferRepository,
	entitlements ActiveJobEntitlementsReader,
	activeJobs func(ctx context.Context, tenantID string) (int, error),
	publisher JobOfferEventPublisher,
	clock Clock,
) *AcceptJobOfferUseCase {
	return &AcceptJobOfferUseCase{
		offers:       offers,
		entitlements: entitlements,
		activeJobs:   activeJobs,
		publisher:    publisher,
		clock:        clock,
	}
}

type AcceptJobOfferResult struct {
	Offer domain.JobOffer
	Job   domain.Job
}

func (u *AcceptJobOfferUseCase) Execute(ctx context.Context, command RespondToJobOfferCommand) (AcceptJobOfferResult, error) {
	offer, found, err := u.offers.FindByID(ctx, command.OfferID)
	if err != nil {
		return AcceptJobOfferResult{}, err
	}
	if !found {
		return AcceptJobOfferResult{}, ErrJobOfferNotFound
	}
	if offer.TargetTenantID != command.TenantID {
		return AcceptJobOfferResult{}, ErrNotYourOffer
	}
	// Checked here for a fast, specific refusal. The transaction re-checks
	// pending/expired itself under a row lock, because a second offer could be
	// answered in the moment between this read and the write.
	if !offer.CanRespond(u.clock.Now()) {
		return AcceptJobOfferResult{}, errors.New("this offer can no longer be responded to")
	}

	// The cap and the reason both come from the server, per the requirements —
	// never a client-supplied count.
	if err := u.assertUnderCap(ctx, command.TenantID); err != nil {
		return AcceptJobOfferResult{}, err
	}

	accepted, err := u.offers.Accept(ctx, command.OfferID, command.TenantID, command.Note, u.clock.Now())
	if err != nil {
		return AcceptJobOfferResult{}, fmt.Errorf("accept job offer: %w", err)
	}

	if u.publisher != nil {
		_ = u.publisher.PublishOfferResponded(ctx, accepted.Offer)
	}

	return AcceptJobOfferResult{Offer: accepted.Offer, Job: accepted.Job}, nil
}

func (u *AcceptJobOfferUseCase) assertUnderCap(ctx context.Context, tenantID string) error {
	if u.entitlements == nil || u.activeJobs == nil {
		return nil
	}

	max, err := u.entitlements.MaxActiveJobs(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("read active job entitlement: %w", err)
	}
	if max == nil {
		return nil
	}

	current, err := u.activeJobs(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("count active jobs: %w", err)
	}
	if current >= *max {
		return ErrOverActiveJobCap
	}
	return nil
}

// ActiveJobCapacityUseCase answers the eligibility banner: a maximum, a
// current count, and — the point the requirements call out — a
// human-readable reason, all decided server-side rather than assembled by the
// client from separate numbers.
type ActiveJobCapacityUseCase struct {
	entitlements ActiveJobEntitlementsReader
	activeJobs   func(ctx context.Context, tenantID string) (int, error)
}

func NewActiveJobCapacityUseCase(
	entitlements ActiveJobEntitlementsReader,
	activeJobs func(ctx context.Context, tenantID string) (int, error),
) *ActiveJobCapacityUseCase {
	return &ActiveJobCapacityUseCase{entitlements: entitlements, activeJobs: activeJobs}
}

type ActiveJobCapacity struct {
	Current  int    `json:"current"`
	Max      *int   `json:"max"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

func (u *ActiveJobCapacityUseCase) Execute(ctx context.Context, tenantID string) (ActiveJobCapacity, error) {
	current, err := u.activeJobs(ctx, tenantID)
	if err != nil {
		return ActiveJobCapacity{}, err
	}

	var max *int
	if u.entitlements != nil {
		max, err = u.entitlements.MaxActiveJobs(ctx, tenantID)
		if err != nil {
			return ActiveJobCapacity{}, err
		}
	}

	if max == nil || current < *max {
		return ActiveJobCapacity{Current: current, Max: max, Eligible: true}, nil
	}
	return ActiveJobCapacity{
		Current:  current,
		Max:      max,
		Eligible: false,
		Reason:   fmt.Sprintf("At the active job limit (%d of %d). Complete or cancel a job before accepting another.", current, *max),
	}, nil
}

// ListJobOffersUseCase serves the Incoming and Past tabs, plus the incoming
// count badge.
type ListJobOffersUseCase struct {
	offers JobOfferRepository
}

func NewListJobOffersUseCase(offers JobOfferRepository) *ListJobOffersUseCase {
	return &ListJobOffersUseCase{offers: offers}
}

type ListJobOffersResult struct {
	Offers        []domain.JobOffer `json:"offers"`
	Total         int               `json:"total"`
	IncomingCount int               `json:"incoming_count"`
}

func (u *ListJobOffersUseCase) Execute(ctx context.Context, query JobOfferQuery) (ListJobOffersResult, error) {
	offers, total, err := u.offers.List(ctx, query)
	if err != nil {
		return ListJobOffersResult{}, err
	}

	// The badge counts every pending offer for this tenant, not the current
	// page or tab — it has to be right even while the operator is looking at
	// Past Offers.
	incoming, err := u.offers.CountPending(ctx, query.TenantID)
	if err != nil {
		return ListJobOffersResult{}, err
	}

	return ListJobOffersResult{Offers: offers, Total: total, IncomingCount: incoming}, nil
}

// ExpireJobOffersUseCase moves offers whose window passed into Past without
// anyone acting — the requirements flag the reference build's bug of leaving
// expired offers sitting in Incoming, and name the fix explicitly.
type ExpireJobOffersUseCase struct {
	offers JobOfferRepository
	clock  Clock
}

func NewExpireJobOffersUseCase(offers JobOfferRepository, clock Clock) *ExpireJobOffersUseCase {
	return &ExpireJobOffersUseCase{offers: offers, clock: clock}
}

const expireJobOffersBatchSize = 200

func (u *ExpireJobOffersUseCase) Execute(ctx context.Context) (int, error) {
	now := u.clock.Now()
	due, err := u.offers.FindExpiring(ctx, now, expireJobOffersBatchSize)
	if err != nil {
		return 0, err
	}

	expired := 0
	for _, offer := range due {
		next, err := offer.Expire(now)
		if err != nil {
			continue
		}
		if err := u.offers.SaveExpired(ctx, next); err != nil {
			return expired, fmt.Errorf("save expired offer: %w", err)
		}
		expired++
	}
	return expired, nil
}
