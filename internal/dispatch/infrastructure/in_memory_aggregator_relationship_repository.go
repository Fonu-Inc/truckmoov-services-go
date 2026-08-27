package infrastructure

import (
	"context"
	"sort"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type InMemoryAggregatorRelationshipRepository struct {
	mu            sync.Mutex
	relationships map[string]domain.AggregatorRelationship
}

func NewInMemoryAggregatorRelationshipRepository() *InMemoryAggregatorRelationshipRepository {
	return &InMemoryAggregatorRelationshipRepository{relationships: map[string]domain.AggregatorRelationship{}}
}

func (r *InMemoryAggregatorRelationshipRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemoryAggregatorRelationshipRepository) Create(_ context.Context, relationship domain.AggregatorRelationship) (domain.AggregatorRelationship, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.relationships[relationship.ID] = relationship
	return relationship, nil
}

func (r *InMemoryAggregatorRelationshipRepository) FindByID(_ context.Context, id string) (domain.AggregatorRelationship, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rel, ok := r.relationships[id]
	return rel, ok, nil
}

func (r *InMemoryAggregatorRelationshipRepository) FindActiveBetween(_ context.Context, aggregatorTenantID, operatorTenantID string) (domain.AggregatorRelationship, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best domain.AggregatorRelationship
	found := false
	for _, rel := range r.relationships {
		if rel.AggregatorTenantID != aggregatorTenantID || rel.OperatorTenantID != operatorTenantID {
			continue
		}
		if rel.Status == domain.RelationshipStatusEnded {
			continue
		}
		if !found || rel.CreatedAt.After(best.CreatedAt) {
			best = rel
			found = true
		}
	}
	return best, found, nil
}

func (r *InMemoryAggregatorRelationshipRepository) ListByAggregator(_ context.Context, aggregatorTenantID string) ([]domain.AggregatorRelationship, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	matched := []domain.AggregatorRelationship{}
	for _, rel := range r.relationships {
		if rel.AggregatorTenantID == aggregatorTenantID {
			matched = append(matched, rel)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	return matched, nil
}

func (r *InMemoryAggregatorRelationshipRepository) ListDistinctAggregatorTenants(_ context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	tenants := []string{}
	for _, rel := range r.relationships {
		if seen[rel.AggregatorTenantID] {
			continue
		}
		seen[rel.AggregatorTenantID] = true
		tenants = append(tenants, rel.AggregatorTenantID)
	}
	return tenants, nil
}

func (r *InMemoryAggregatorRelationshipRepository) Save(_ context.Context, relationship domain.AggregatorRelationship) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.relationships[relationship.ID] = relationship
	return nil
}
