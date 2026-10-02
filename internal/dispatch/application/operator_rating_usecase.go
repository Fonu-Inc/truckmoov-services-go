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
	ErrJobNotSubDispatched = errors.New("this job was not sub-dispatched from your organisation to this operator")
)

// RateOperatorUseCase captures the cargo owner's (aggregator's) rating of the
// operator who ran a delivered job — the only source of rating data on the
// network overview screen.
type RateOperatorUseCase struct {
	offers  JobOfferRepository
	jobs    JobRepository
	ratings OperatorRatingRepository
	clock   Clock
	idGen   IDGenerator
}

func NewRateOperatorUseCase(
	offers JobOfferRepository,
	jobs JobRepository,
	ratings OperatorRatingRepository,
	clock Clock,
	idGen IDGenerator,
) *RateOperatorUseCase {
	return &RateOperatorUseCase{offers: offers, jobs: jobs, ratings: ratings, clock: clock, idGen: idGen}
}

type RateOperatorCommand struct {
	RaterTenantID    string
	OperatorTenantID string
	JobID            string
	Value            int
	Note             string
}

func (u *RateOperatorUseCase) Execute(ctx context.Context, command RateOperatorCommand) (domain.OperatorRating, error) {
	if _, found, err := u.ratings.FindByJob(ctx, command.JobID); err != nil {
		return domain.OperatorRating{}, err
	} else if found {
		return domain.OperatorRating{}, ErrRatingAlreadyExists
	}

	job, found := u.jobs.FindByTenantAndID(command.OperatorTenantID, command.JobID)
	if !found {
		return domain.OperatorRating{}, ErrSubDispatchJobNotFound
	}
	if job.Status != domain.StatusDelivered {
		return domain.OperatorRating{}, ErrJobNotDelivered
	}

	accepted, err := u.offers.ListAccepted(ctx, command.RaterTenantID, command.OperatorTenantID)
	if err != nil {
		return domain.OperatorRating{}, fmt.Errorf("verify sub-dispatch history: %w", err)
	}
	sentByThisAggregator := false
	for _, offer := range accepted {
		if offer.JobID == command.JobID {
			sentByThisAggregator = true
			break
		}
	}
	if !sentByThisAggregator {
		return domain.OperatorRating{}, ErrJobNotSubDispatched
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
