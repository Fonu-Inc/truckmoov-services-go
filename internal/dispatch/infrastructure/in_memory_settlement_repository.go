package infrastructure

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type InMemorySettlementRepository struct {
	mu          sync.Mutex
	settlements map[string]domain.Settlement
}

func NewInMemorySettlementRepository() *InMemorySettlementRepository {
	return &InMemorySettlementRepository{settlements: map[string]domain.Settlement{}}
}

func (r *InMemorySettlementRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemorySettlementRepository) Create(_ context.Context, settlement domain.Settlement) (domain.Settlement, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settlements[settlement.ID] = settlement
	return settlement, nil
}

func (r *InMemorySettlementRepository) FindByID(_ context.Context, id string) (domain.Settlement, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.settlements[id]
	return s, ok, nil
}

func (r *InMemorySettlementRepository) FindForPeriod(_ context.Context, aggregatorTenantID, operatorTenantID string, periodStart time.Time) (domain.Settlement, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.settlements {
		if s.AggregatorTenantID == aggregatorTenantID && s.OperatorTenantID == operatorTenantID && s.PeriodStart.Equal(periodStart) {
			return s, true, nil
		}
	}
	return domain.Settlement{}, false, nil
}

func (r *InMemorySettlementRepository) ListByAggregator(_ context.Context, aggregatorTenantID string, limit, offset int) ([]domain.Settlement, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	matched := []domain.Settlement{}
	for _, s := range r.settlements {
		if s.AggregatorTenantID == aggregatorTenantID {
			matched = append(matched, s)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	total := len(matched)

	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []domain.Settlement{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return matched[offset:end], total, nil
}

func (r *InMemorySettlementRepository) Save(_ context.Context, settlement domain.Settlement) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settlements[settlement.ID] = settlement
	return nil
}
