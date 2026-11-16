package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

var (
	ErrRatingAlreadyExists = errors.New("this job has already been rated")
	ErrJobNotDelivered     = errors.New("only a delivered job can be rated")
)

// RateOperatorUseCase captures the cargo owner's (aggregator's) rating of the
// operator who ran a delivered job — the only source of rating data on the
// network overview screen.
type RateOperatorUseCase struct {
	relationships AggregatorRelationshipRepository
	offers        JobOfferRepository
	jobs          JobRepository
	ratings       OperatorRatingRepository
	clock         Clock
	idGen         IDGenerator
}

func NewRateOperatorUseCase(
	relationships AggregatorRelationshipRepository,
	offers JobOfferRepository,
	jobs JobRepository,
	ratings OperatorRatingRepository,
	clock Clock,
	idGen IDGenerator,
) *RateOperatorUseCase {
	return &RateOperatorUseCase{relationships: relationships, offers: offers, jobs: jobs, ratings: ratings, clock: clock, idGen: idGen}
}

type RateOperatorCommand struct {
	RaterTenantID    string
	OperatorTenantID string
	JobID            string
	Value            int
	Note             string
}

func (u *RateOperatorUseCase) Execute(ctx context.Context, command RateOperatorCommand) (domain.OperatorRating, error) {
	// Entitlement first, and every miss looks the same, so a rater cannot
	// probe for other tenants' jobs or links.
	entitled, err := u.raterSubDispatched(ctx, command)
	if err != nil {
		return domain.OperatorRating{}, err
	}
	if !entitled {
		return domain.OperatorRating{}, ErrSubDispatchJobNotFound
	}

	if _, found, err := u.ratings.FindByJob(ctx, command.OperatorTenantID, command.JobID); err != nil {
		return domain.OperatorRating{}, err
	} else if found {
		return domain.OperatorRating{}, ErrRatingAlreadyExists
	}

	job, err := u.jobs.FindByTenantAndID(command.OperatorTenantID, command.JobID)
	if errors.Is(err, ErrJobNotFound) {
		return domain.OperatorRating{}, ErrSubDispatchJobNotFound
	}
	if err != nil {
		return domain.OperatorRating{}, fmt.Errorf("load operator job: %w", err)
	}
	if job.Status != domain.StatusDelivered {
		return domain.OperatorRating{}, ErrJobNotDelivered
	}

	rating, err := domain.NewOperatorRating(domain.OperatorRating{
		ID:               "rating-" + u.idGen.New(),
		JobID:            command.JobID,
		RaterTenantID:    command.RaterTenantID,
		OperatorTenantID: command.OperatorTenantID,
		Value:            command.Value,
		Note:             command.Note,
		CreatedAt:        u.clock.Now(),
	})
	if err != nil {
		return domain.OperatorRating{}, err
	}

	return u.ratings.Create(ctx, rating)
}

// raterSubDispatched is whether the rater has, or had, an accepted link with
// the operator and an accepted offer that handed the operator this job.
func (u *RateOperatorUseCase) raterSubDispatched(ctx context.Context, command RateOperatorCommand) (bool, error) {
	relationships, err := u.relationships.ListByAggregator(ctx, command.RaterTenantID)
	if err != nil {
		return false, fmt.Errorf("verify relationship: %w", err)
	}
	linked := false
	for _, relationship := range relationships {
		if relationship.OperatorTenantID == command.OperatorTenantID && relationship.Status != domain.RelationshipStatusPending {
			linked = true
			break
		}
	}
	if !linked {
		return false, nil
	}

	accepted, err := u.offers.ListAccepted(ctx, command.RaterTenantID, command.OperatorTenantID)
	if err != nil {
		return false, fmt.Errorf("verify sub-dispatch history: %w", err)
	}
	for _, offer := range accepted {
		if offer.CarrierJob() == command.JobID {
			return true, nil
		}
	}
	return false, nil
}
