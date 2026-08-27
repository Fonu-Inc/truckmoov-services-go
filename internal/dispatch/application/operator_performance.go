package application

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// OperatorPerformanceEntry is one linked operator's row on the "Operator
// Performance (last 30 days)" table.
type OperatorPerformanceEntry struct {
	OperatorTenantID  string  `json:"operator_tenant_id"`
	Jobs              int     `json:"jobs"`
	OnTimeRatePct     float64 `json:"on_time_rate_pct"`
	AcceptanceRatePct float64 `json:"acceptance_rate_pct"`
	RevenueNgn        float64 `json:"revenue_ngn"`
	Rating            float64 `json:"rating"`
}

// OperatorPerformanceUseCase computes, per linked operator, how they did
// against jobs this aggregator sent them in the trailing window.
type OperatorPerformanceUseCase struct {
	relationships AggregatorRelationshipRepository
	offers        JobOfferRepository
	jobs          JobByIDLookup
	ratings       OperatorRatingRepository
	clock         Clock
	window        time.Duration
}

func NewOperatorPerformanceUseCase(
	relationships AggregatorRelationshipRepository,
	offers JobOfferRepository,
	jobs JobByIDLookup,
	ratings OperatorRatingRepository,
	clock Clock,
) *OperatorPerformanceUseCase {
	return &OperatorPerformanceUseCase{
		relationships: relationships, offers: offers, jobs: jobs, ratings: ratings,
		clock: clock, window: 30 * 24 * time.Hour,
	}
}

func (u *OperatorPerformanceUseCase) Execute(ctx context.Context, aggregatorTenantID string) ([]OperatorPerformanceEntry, error) {
	relationships, err := u.relationships.ListByAggregator(ctx, aggregatorTenantID)
	if err != nil {
		return nil, err
	}

	since := u.clock.Now().Add(-u.window)
	entries := make([]OperatorPerformanceEntry, 0, len(relationships))

	allOffers, err := listAllOfferingOffers(ctx, u.offers, aggregatorTenantID)
	if err != nil {
		return nil, fmt.Errorf("list offers for performance: %w", err)
	}

	for _, relationship := range relationships {
		if !relationship.IsActive() {
			continue
		}

		var windowOffers, responded, accepted int
		var onTimeDeliveries, deliveredCount int
		var revenue float64

		for _, offer := range allOffers {
			if offer.TargetTenantID != relationship.OperatorTenantID {
				continue
			}
			if offer.CreatedAt.Before(since) {
				continue
			}
			windowOffers++
			if offer.Status != domain.OfferStatusPending {
				responded++
			}
			if offer.Status != domain.OfferStatusAccepted {
				continue
			}
			accepted++

			job, found := u.jobs.FindByID(offer.JobID)
			if !found {
				continue
			}
			revenue += offer.OfferedRateNgn
			if job.Status != domain.StatusDelivered {
				continue
			}
			deliveredCount++
			if deliveredWithinDeadline(job) {
				onTimeDeliveries++
			}
		}

		entry := OperatorPerformanceEntry{
			OperatorTenantID: relationship.OperatorTenantID,
			Jobs:             windowOffers,
			RevenueNgn:       revenue,
		}
		if responded > 0 {
			entry.AcceptanceRatePct = roundToTwoDecimals(float64(accepted) / float64(responded) * 100)
		}
		if deliveredCount > 0 {
			entry.OnTimeRatePct = roundToTwoDecimals(float64(onTimeDeliveries) / float64(deliveredCount) * 100)
		}
		if u.ratings != nil {
			if avg, count, err := u.ratings.AverageForOperator(ctx, relationship.OperatorTenantID); err == nil && count > 0 {
				entry.Rating = avg
			}
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// deliveredWithinDeadline reports whether a delivered job's delivery
// timestamp (its "job_status_updated" timeline entry to delivered) fell at
// or before the job's own deadline. A job with no deadline set counts as
// on time — there was nothing to miss.
func deliveredWithinDeadline(job domain.Job) bool {
	if job.Deadline == nil {
		return true
	}
	deliveredAt := deliveredTimestamp(job)
	if deliveredAt == nil {
		return true
	}
	return !deliveredAt.After(*job.Deadline)
}

func deliveredTimestamp(job domain.Job) *time.Time {
	for i := len(job.Timeline) - 1; i >= 0; i-- {
		entry := job.Timeline[i]
		if entry.Type != "job_status_updated" && entry.Type != "driver_status_confirmed" {
			continue
		}
		if entry.Metadata["to_status"] == string(domain.StatusDelivered) || entry.Metadata["requested_status"] == string(domain.StatusDelivered) {
			occurredAt := entry.OccurredAt
			return &occurredAt
		}
	}
	return nil
}

// TopPerformingOperatorsUseCase ranks linked operators by completed job
// volume, tie-broken by rating — the requirements' own ordering.
type TopPerformingOperatorsUseCase struct {
	relationships AggregatorRelationshipRepository
	offers        JobOfferRepository
	jobs          JobByIDLookup
	ratings       OperatorRatingRepository
}

func NewTopPerformingOperatorsUseCase(
	relationships AggregatorRelationshipRepository,
	offers JobOfferRepository,
	jobs JobByIDLookup,
	ratings OperatorRatingRepository,
) *TopPerformingOperatorsUseCase {
	return &TopPerformingOperatorsUseCase{relationships: relationships, offers: offers, jobs: jobs, ratings: ratings}
}

type TopOperatorEntry struct {
	Rank             int     `json:"rank"`
	OperatorTenantID string  `json:"operator_tenant_id"`
	CompletedJobs    int     `json:"completed_jobs"`
	Rating           float64 `json:"rating"`
}

func (u *TopPerformingOperatorsUseCase) Execute(ctx context.Context, aggregatorTenantID string) ([]TopOperatorEntry, error) {
	relationships, err := u.relationships.ListByAggregator(ctx, aggregatorTenantID)
	if err != nil {
		return nil, err
	}
	allOffers, err := listAllOfferingOffers(ctx, u.offers, aggregatorTenantID)
	if err != nil {
		return nil, fmt.Errorf("list offers for ranking: %w", err)
	}

	entries := make([]TopOperatorEntry, 0, len(relationships))
	for _, relationship := range relationships {
		if !relationship.IsActive() {
			continue
		}

		completed := 0
		for _, offer := range allOffers {
			if offer.TargetTenantID != relationship.OperatorTenantID || offer.Status != domain.OfferStatusAccepted {
				continue
			}
			job, found := u.jobs.FindByID(offer.JobID)
			if found && job.Status == domain.StatusDelivered {
				completed++
			}
		}

		entry := TopOperatorEntry{OperatorTenantID: relationship.OperatorTenantID, CompletedJobs: completed}
		if u.ratings != nil {
			if avg, count, err := u.ratings.AverageForOperator(ctx, relationship.OperatorTenantID); err == nil && count > 0 {
				entry.Rating = avg
			}
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CompletedJobs != entries[j].CompletedJobs {
			return entries[i].CompletedJobs > entries[j].CompletedJobs
		}
		return entries[i].Rating > entries[j].Rating
	})
	for i := range entries {
		entries[i].Rank = i + 1
	}

	return entries, nil
}
