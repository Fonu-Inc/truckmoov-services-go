package application

import (
	"context"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// ListAggregatorActivityUseCase serves the Live Network Activity feed.
type ListAggregatorActivityUseCase struct {
	activity AggregatorActivityRepository
}

func NewListAggregatorActivityUseCase(activity AggregatorActivityRepository) *ListAggregatorActivityUseCase {
	return &ListAggregatorActivityUseCase{activity: activity}
}

const defaultAggregatorActivityLimit = 50

func (u *ListAggregatorActivityUseCase) Execute(ctx context.Context, aggregatorTenantID string) ([]domain.AggregatorActivityEntry, error) {
	return u.activity.ListByAggregator(ctx, aggregatorTenantID, defaultAggregatorActivityLimit)
}
