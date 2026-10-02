package application

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// AggregatorRelationshipRepository owns the one thing every panel on the
// network overview screen depends on: which operators are linked to which
// aggregator, and what that link grants.
type AggregatorRelationshipRepository interface {
	EnsureSchema(ctx context.Context) error
	Create(ctx context.Context, relationship domain.AggregatorRelationship) (domain.AggregatorRelationship, error)
	FindByID(ctx context.Context, id string) (domain.AggregatorRelationship, bool, error)
	// FindActiveBetween finds an existing active or pending relationship
	// between this pair, so Add Operator can refuse a duplicate.
	FindActiveBetween(ctx context.Context, aggregatorTenantID, operatorTenantID string) (domain.AggregatorRelationship, bool, error)
	ListByAggregator(ctx context.Context, aggregatorTenantID string) ([]domain.AggregatorRelationship, error)
	// ListDistinctAggregatorTenants enumerates every tenant that has ever
	// linked an operator — there is no separate tenant/organisation registry
	// in this service, so this is how the weekly settlement sweep finds who
	// to close a week for.
	ListDistinctAggregatorTenants(ctx context.Context) ([]string, error)
	Save(ctx context.Context, relationship domain.AggregatorRelationship) error
}

// OperatorRatingRepository is the only source of rating data on the
// screen — no other part of the system captures one.
type OperatorRatingRepository interface {
	EnsureSchema(ctx context.Context) error
	Create(ctx context.Context, rating domain.OperatorRating) (domain.OperatorRating, error)
	FindByJob(ctx context.Context, operatorTenantID, jobID string) (domain.OperatorRating, bool, error)
	// AverageForOperator returns the mean rating and how many ratings it is
	// built from. Zero ratings returns (0, 0, nil) — a caller must not treat a
	// zero average as a real 0-star rating.
	AverageForOperator(ctx context.Context, operatorTenantID string) (float64, int, error)
}

// SettlementRepository is the immutable ledger of money moved between an
// aggregator and its linked operators.
type SettlementRepository interface {
	EnsureSchema(ctx context.Context) error
	Create(ctx context.Context, settlement domain.Settlement) (domain.Settlement, error)
	FindByID(ctx context.Context, id string) (domain.Settlement, bool, error)
	// FindForPeriod checks whether this pair's period has already been closed,
	// so the weekly sweep never creates two settlements for the same week.
	FindForPeriod(ctx context.Context, aggregatorTenantID, operatorTenantID string, periodStart time.Time) (domain.Settlement, bool, error)
	ListByAggregator(ctx context.Context, aggregatorTenantID string, limit, offset int) ([]domain.Settlement, int, error)
	Save(ctx context.Context, settlement domain.Settlement) error
}

// AggregatorActivityRepository backs the Live Network Activity feed — an
// append-only log written directly by the use case that caused the event,
// rather than a separate NATS-consuming projector. Simpler, and each write
// happens in the same request that already knows every field the row needs.
type AggregatorActivityRepository interface {
	EnsureSchema(ctx context.Context) error
	Append(ctx context.Context, entry domain.AggregatorActivityEntry) error
	ListByAggregator(ctx context.Context, aggregatorTenantID string, limit int) ([]domain.AggregatorActivityEntry, error)
}

// FleetCountsReader answers "how many vehicles/drivers does this tenant
// have" — fleet's data, asked for rather than duplicated, the same way
// AuthEntitlementsReader asks auth for plan quotas.
type FleetCountsReader interface {
	TenantCounts(ctx context.Context, tenantID string) (vehicles, drivers int, err error)
}

// ComplianceStatusReader asks fleet for a tenant's compliance-document
// standing (insurance, roadworthiness). Fleet's data, asked for rather than
// duplicated — the same pattern as FleetCountsReader. KYC is deliberately not
// included: nothing in the platform recomputes a tenant's overall KYC status
// today (see TRU-25), so this reader has no honest answer to give for it yet.
type ComplianceStatusReader interface {
	Snapshot(ctx context.Context, tenantID string) (ComplianceSnapshot, error)
}

// ComplianceSnapshot mirrors fleet's compliance-documents summary: counts by
// standing plus a derived rate, not broken out per document type.
type ComplianceSnapshot struct {
	Total                 int
	Valid                 int
	ExpiringSoon          int
	Expired               int
	ComplianceRatePercent int
}

// offeredJob finds the job an offer is about under its current owner: the
// offering tenant until the offer is accepted, the carrier after.
func offeredJob(jobs JobRepository, offer domain.JobOffer) (domain.Job, bool) {
	owner := offer.OfferingTenantID
	if offer.Status == domain.OfferStatusAccepted {
		owner = offer.TargetTenantID
	}
	return jobs.FindByTenantAndID(owner, offer.JobID)
}
