package application

import (
	"context"
	"fmt"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// fakeRelationshipRepository is a minimal, non-concurrent-safe stand-in —
// same rationale as fakeOfferRepository: test the use cases' own logic, not
// repository atomicity.
type fakeRelationshipRepository struct {
	relationships map[string]domain.AggregatorRelationship
}

func newFakeRelationshipRepository() *fakeRelationshipRepository {
	return &fakeRelationshipRepository{relationships: map[string]domain.AggregatorRelationship{}}
}

func (r *fakeRelationshipRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakeRelationshipRepository) Create(_ context.Context, rel domain.AggregatorRelationship) (domain.AggregatorRelationship, error) {
	r.relationships[rel.ID] = rel
	return rel, nil
}

func (r *fakeRelationshipRepository) FindByID(_ context.Context, id string) (domain.AggregatorRelationship, bool, error) {
	rel, ok := r.relationships[id]
	return rel, ok, nil
}

func (r *fakeRelationshipRepository) FindActiveBetween(_ context.Context, aggregatorTenantID, operatorTenantID string) (domain.AggregatorRelationship, bool, error) {
	for _, rel := range r.relationships {
		if rel.AggregatorTenantID == aggregatorTenantID && rel.OperatorTenantID == operatorTenantID && rel.Status != domain.RelationshipStatusEnded {
			return rel, true, nil
		}
	}
	return domain.AggregatorRelationship{}, false, nil
}

func (r *fakeRelationshipRepository) ListByAggregator(_ context.Context, aggregatorTenantID string) ([]domain.AggregatorRelationship, error) {
	out := []domain.AggregatorRelationship{}
	for _, rel := range r.relationships {
		if rel.AggregatorTenantID == aggregatorTenantID {
			out = append(out, rel)
		}
	}
	return out, nil
}

func (r *fakeRelationshipRepository) ListDistinctAggregatorTenants(_ context.Context) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, rel := range r.relationships {
		if !seen[rel.AggregatorTenantID] {
			seen[rel.AggregatorTenantID] = true
			out = append(out, rel.AggregatorTenantID)
		}
	}
	return out, nil
}

func (r *fakeRelationshipRepository) Save(_ context.Context, rel domain.AggregatorRelationship) error {
	r.relationships[rel.ID] = rel
	return nil
}

type fakeRatingRepository struct {
	ratings map[string]domain.OperatorRating // keyed by operator tenant + job id
}

func newFakeRatingRepository() *fakeRatingRepository {
	return &fakeRatingRepository{ratings: map[string]domain.OperatorRating{}}
}

func (r *fakeRatingRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakeRatingRepository) Create(_ context.Context, rating domain.OperatorRating) (domain.OperatorRating, error) {
	r.ratings[rating.OperatorTenantID+"/"+rating.JobID] = rating
	return rating, nil
}

func (r *fakeRatingRepository) FindByJob(_ context.Context, operatorTenantID, jobID string) (domain.OperatorRating, bool, error) {
	rating, ok := r.ratings[operatorTenantID+"/"+jobID]
	return rating, ok, nil
}

func (r *fakeRatingRepository) AverageForOperator(_ context.Context, operatorTenantID string) (float64, int, error) {
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
	return sum / float64(count), count, nil
}

type fakeSettlementRepository struct {
	settlements map[string]domain.Settlement
}

func newFakeSettlementRepository() *fakeSettlementRepository {
	return &fakeSettlementRepository{settlements: map[string]domain.Settlement{}}
}

func (r *fakeSettlementRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakeSettlementRepository) Create(_ context.Context, s domain.Settlement) (domain.Settlement, error) {
	r.settlements[s.ID] = s
	return s, nil
}

func (r *fakeSettlementRepository) FindByID(_ context.Context, id string) (domain.Settlement, bool, error) {
	s, ok := r.settlements[id]
	return s, ok, nil
}

func (r *fakeSettlementRepository) FindForPeriod(_ context.Context, aggregatorTenantID, operatorTenantID string, periodStart time.Time) (domain.Settlement, bool, error) {
	for _, s := range r.settlements {
		if s.AggregatorTenantID == aggregatorTenantID && s.OperatorTenantID == operatorTenantID && s.PeriodStart.Equal(periodStart) {
			return s, true, nil
		}
	}
	return domain.Settlement{}, false, nil
}

func (r *fakeSettlementRepository) ListByAggregator(_ context.Context, aggregatorTenantID string, limit, offset int) ([]domain.Settlement, int, error) {
	out := []domain.Settlement{}
	for _, s := range r.settlements {
		if s.AggregatorTenantID == aggregatorTenantID {
			out = append(out, s)
		}
	}
	return out, len(out), nil
}

func (r *fakeSettlementRepository) Save(_ context.Context, s domain.Settlement) error {
	r.settlements[s.ID] = s
	return nil
}

type fakeActivityRepository struct {
	entries []domain.AggregatorActivityEntry
}

func newFakeActivityRepository() *fakeActivityRepository {
	return &fakeActivityRepository{}
}

func (r *fakeActivityRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakeActivityRepository) Append(_ context.Context, entry domain.AggregatorActivityEntry) error {
	r.entries = append(r.entries, entry)
	return nil
}

func (r *fakeActivityRepository) ListByAggregator(_ context.Context, aggregatorTenantID string, limit int) ([]domain.AggregatorActivityEntry, error) {
	out := []domain.AggregatorActivityEntry{}
	for _, entry := range r.entries {
		if entry.AggregatorTenantID == aggregatorTenantID {
			out = append(out, entry)
		}
	}
	return out, nil
}

type fakeFleetCounts struct {
	vehicles, drivers int
}

func (f fakeFleetCounts) TenantCounts(context.Context, string) (int, int, error) {
	return f.vehicles, f.drivers, nil
}

type fakeComplianceStatusReader struct {
	byTenant map[string]ComplianceSnapshot
}

func newFakeComplianceStatusReader() *fakeComplianceStatusReader {
	return &fakeComplianceStatusReader{byTenant: map[string]ComplianceSnapshot{}}
}

func (f *fakeComplianceStatusReader) Snapshot(_ context.Context, tenantID string) (ComplianceSnapshot, error) {
	return f.byTenant[tenantID], nil
}

type sequentialAggregatorIDs struct{ n int }

func (g *sequentialAggregatorIDs) New() string {
	g.n++
	return fmt.Sprintf("id-%d", g.n)
}
