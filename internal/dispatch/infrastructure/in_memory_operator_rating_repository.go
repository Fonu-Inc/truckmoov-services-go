package infrastructure

import (
	"context"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type InMemoryOperatorRatingRepository struct {
	mu      sync.Mutex
	ratings map[string]domain.OperatorRating // keyed by job id
}

func NewInMemoryOperatorRatingRepository() *InMemoryOperatorRatingRepository {
	return &InMemoryOperatorRatingRepository{ratings: map[string]domain.OperatorRating{}}
}

func (r *InMemoryOperatorRatingRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemoryOperatorRatingRepository) Create(_ context.Context, rating domain.OperatorRating) (domain.OperatorRating, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ratings[rating.JobID] = rating
	return rating, nil
}

func (r *InMemoryOperatorRatingRepository) FindByJob(_ context.Context, jobID string) (domain.OperatorRating, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rating, ok := r.ratings[jobID]
	return rating, ok, nil
}

func (r *InMemoryOperatorRatingRepository) AverageForOperator(_ context.Context, operatorTenantID string) (float64, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sum float64
	var count int
	for _, rating := range r.ratings {
		if rating.OperatorTenantID != operatorTenantID {
			continue
		}
		sum += float64(rating.Value)
		count++
	}
	if count == 0 {
		return 0, 0, nil
	}
	return roundToTwoDecimalsInfra(sum / float64(count)), count, nil
}
