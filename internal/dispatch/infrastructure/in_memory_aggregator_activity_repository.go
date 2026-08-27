package infrastructure

import (
	"context"
	"sort"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type InMemoryAggregatorActivityRepository struct {
	mu      sync.Mutex
	entries []domain.AggregatorActivityEntry
}

func NewInMemoryAggregatorActivityRepository() *InMemoryAggregatorActivityRepository {
	return &InMemoryAggregatorActivityRepository{}
}

func (r *InMemoryAggregatorActivityRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemoryAggregatorActivityRepository) Append(_ context.Context, entry domain.AggregatorActivityEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return nil
}

func (r *InMemoryAggregatorActivityRepository) ListByAggregator(_ context.Context, aggregatorTenantID string, limit int) ([]domain.AggregatorActivityEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	matched := []domain.AggregatorActivityEntry{}
	for _, entry := range r.entries {
		if entry.AggregatorTenantID == aggregatorTenantID {
			matched = append(matched, entry)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].OccurredAt.After(matched[j].OccurredAt) })
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}
