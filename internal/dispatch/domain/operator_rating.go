package domain

import (
	"errors"
	"strings"
	"time"
)

// OperatorRating is one cargo owner's (or aggregator's) rating of the
// operator who ran a delivered job. This is the only source for the average
// rating, performance-table rating and top-operators ranking the network
// overview screen shows — there is nowhere else in the system a rating is
// captured.
type OperatorRating struct {
	ID               string
	JobID            string
	RaterTenantID    string
	OperatorTenantID string
	Value            int
	Note             string
	CreatedAt        time.Time
}

func NewOperatorRating(params OperatorRating) (OperatorRating, error) {
	if strings.TrimSpace(params.ID) == "" {
		return OperatorRating{}, errors.New("rating id is required")
	}
	if strings.TrimSpace(params.JobID) == "" {
		return OperatorRating{}, errors.New("job id is required")
	}
	if strings.TrimSpace(params.RaterTenantID) == "" {
		return OperatorRating{}, errors.New("rater tenant id is required")
	}
	if strings.TrimSpace(params.OperatorTenantID) == "" {
		return OperatorRating{}, errors.New("operator tenant id is required")
	}
	if strings.TrimSpace(params.RaterTenantID) == strings.TrimSpace(params.OperatorTenantID) {
		return OperatorRating{}, errors.New("a tenant cannot rate itself")
	}
	if params.Value < 1 || params.Value > 5 {
		return OperatorRating{}, errors.New("rating value must be between 1 and 5")
	}
	if params.CreatedAt.IsZero() {
		return OperatorRating{}, errors.New("created_at is required")
	}

	return OperatorRating{
		ID:               strings.TrimSpace(params.ID),
		JobID:            strings.TrimSpace(params.JobID),
		RaterTenantID:    strings.TrimSpace(params.RaterTenantID),
		OperatorTenantID: strings.TrimSpace(params.OperatorTenantID),
		Value:            params.Value,
		Note:             strings.TrimSpace(params.Note),
		CreatedAt:        params.CreatedAt.UTC(),
	}, nil
}
