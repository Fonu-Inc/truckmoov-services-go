package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// listAllAcceptedByAggregatorOffering pages through every offer this
// aggregator has ever made. Bounded at 500: an MVP-scale read, matching how
// activeJobCounter elsewhere in this service loops a tenant's jobs in Go
// rather than pushing the aggregation into SQL.
const aggregatorOfferScanLimit = 500

func listAllOfferingOffers(ctx context.Context, offers JobOfferRepository, aggregatorTenantID string) ([]domain.JobOffer, error) {
	all := []domain.JobOffer{}
	page := 1
	for {
		batch, total, err := offers.List(ctx, JobOfferQuery{TenantID: aggregatorTenantID, Role: "offering", Page: page, Limit: 100})
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(all) >= total || len(batch) == 0 || len(all) >= aggregatorOfferScanLimit {
			break
		}
		page++
	}
	return all, nil
}

// SummaryFiguresUseCase answers the eight aggregate figures shown at the top
// of the network overview screen, each without loading the full list behind it.
type SummaryFiguresUseCase struct {
	relationships AggregatorRelationshipRepository
	jobs          JobRepository
	fleetCounts   FleetCountsReader
	ratings       OperatorRatingRepository
}

func NewSummaryFiguresUseCase(
	relationships AggregatorRelationshipRepository,
	jobs JobRepository,
	fleetCounts FleetCountsReader,
	ratings OperatorRatingRepository,
) *SummaryFiguresUseCase {
	return &SummaryFiguresUseCase{relationships: relationships, jobs: jobs, fleetCounts: fleetCounts, ratings: ratings}
}

type NetworkSummary struct {
	LinkedOperators    int     `json:"linked_operators"`
	TotalRelationships int     `json:"total_relationships"`
	ActiveJobs         int     `json:"active_jobs"`
	Completed          int     `json:"completed"`
	NetworkVehicles    int     `json:"network_vehicles"`
	NetworkDrivers     int     `json:"network_drivers"`
	AvgOperatorRating  float64 `json:"avg_operator_rating"`
	RegionsCovered     int     `json:"regions_covered"`
}

func (u *SummaryFiguresUseCase) Execute(ctx context.Context, aggregatorTenantID string) (NetworkSummary, error) {
	relationships, err := u.relationships.ListByAggregator(ctx, aggregatorTenantID)
	if err != nil {
		return NetworkSummary{}, err
	}

	summary := NetworkSummary{}
	regions := map[domain.Region]bool{}
	var ratingSum float64
	var ratedOperators int

	for _, relationship := range relationships {
		if relationship.Status != domain.RelationshipStatusEnded {
			summary.TotalRelationships++
		}
		if !relationship.IsActive() {
			continue
		}
		summary.LinkedOperators++
		for _, r := range relationship.Regions {
			regions[r] = true
		}

		if u.fleetCounts != nil {
			if vehicles, drivers, err := u.fleetCounts.TenantCounts(ctx, relationship.OperatorTenantID); err == nil {
				summary.NetworkVehicles += vehicles
				summary.NetworkDrivers += drivers
			}
		}

		if u.jobs != nil {
			operatorJobs, err := u.jobs.FindByTenant(relationship.OperatorTenantID)
			if err != nil {
				return NetworkSummary{}, fmt.Errorf("list operator jobs: %w", err)
			}
			for _, job := range operatorJobs {
				if job.IsActive() {
					summary.ActiveJobs++
				}
				if job.Status == domain.StatusDelivered {
					summary.Completed++
				}
			}
		}

		if u.ratings != nil {
			if avg, count, err := u.ratings.AverageForOperator(ctx, relationship.OperatorTenantID); err == nil && count > 0 {
				ratingSum += avg
				ratedOperators++
			}
		}
	}

	summary.RegionsCovered = len(regions)
	if ratedOperators > 0 {
		summary.AvgOperatorRating = roundToTwoDecimals(ratingSum / float64(ratedOperators))
	}

	return summary, nil
}

func roundToTwoDecimals(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}

// SubDispatchStatus is the queue's own simplified status vocabulary — a
// summary over Job + JobOffer state, not a status either of those stores
// directly.
type SubDispatchStatus string

const (
	SubDispatchPendingAssignment SubDispatchStatus = "pending_assignment"
	SubDispatchAssigned          SubDispatchStatus = "assigned"
	SubDispatchInTransit         SubDispatchStatus = "in_transit"
	SubDispatchDelivered         SubDispatchStatus = "delivered"
)

type SubDispatchQueueEntry struct {
	JobID      string            `json:"job_id"`
	Reference  string            `json:"reference"`
	Route      string            `json:"route"`
	OperatorID string            `json:"operator_id,omitempty"`
	ValueNgn   float64           `json:"value_ngn"`
	Status     SubDispatchStatus `json:"status"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// SubDispatchQueueUseCase lists every job this aggregator has ever sub-
// dispatched (or could) — jobs it still owns unassigned, plus jobs it has
// offered out, wherever those jobs currently live.
type SubDispatchQueueUseCase struct {
	offers JobOfferRepository
	jobs   JobRepository
}

func NewSubDispatchQueueUseCase(offers JobOfferRepository, jobs JobRepository) *SubDispatchQueueUseCase {
	return &SubDispatchQueueUseCase{offers: offers, jobs: jobs}
}

func (u *SubDispatchQueueUseCase) Execute(ctx context.Context, aggregatorTenantID string) ([]SubDispatchQueueEntry, error) {
	ownJobs, err := u.jobs.FindByTenant(aggregatorTenantID)
	if err != nil {
		return nil, fmt.Errorf("list own jobs: %w", err)
	}
	allOffers, err := listAllOfferingOffers(ctx, u.offers, aggregatorTenantID)
	if err != nil {
		return nil, fmt.Errorf("list sub-dispatch offers: %w", err)
	}

	latestOfferByJob := map[string]domain.JobOffer{}
	for _, offer := range allOffers {
		existing, ok := latestOfferByJob[offer.JobID]
		if !ok || offer.CreatedAt.After(existing.CreatedAt) {
			latestOfferByJob[offer.JobID] = offer
		}
	}

	entries := map[string]SubDispatchQueueEntry{}

	for _, job := range ownJobs {
		if job.Status == domain.StatusCancelled {
			continue
		}
		entries[job.ID] = SubDispatchQueueEntry{
			JobID: job.ID, Reference: job.Reference, Route: routeSummary(job),
			ValueNgn: job.AmountNgn, Status: SubDispatchPendingAssignment, UpdatedAt: job.UpdatedAt,
		}
	}

	for jobID, offer := range latestOfferByJob {
		entry, exists := entries[jobID]
		if !exists {
			job, err := offeredJob(u.jobs, offer)
			if errors.Is(err, ErrJobNotFound) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("load offered job: %w", err)
			}
			entry = SubDispatchQueueEntry{JobID: jobID, Reference: job.Reference, Route: routeSummary(job), ValueNgn: job.AmountNgn}
		}

		switch offer.Status {
		case domain.OfferStatusPending:
			entry.Status = SubDispatchAssigned
			entry.OperatorID = offer.TargetTenantID
			entry.UpdatedAt = offer.UpdatedAt
		case domain.OfferStatusAccepted:
			entry.OperatorID = offer.TargetTenantID
			job, err := offeredJob(u.jobs, offer)
			if err != nil && !errors.Is(err, ErrJobNotFound) {
				return nil, fmt.Errorf("load offered job: %w", err)
			}
			if err == nil {
				entry.ValueNgn = job.AmountNgn
				entry.UpdatedAt = job.UpdatedAt
				if job.Status == domain.StatusDelivered {
					entry.Status = SubDispatchDelivered
				} else {
					entry.Status = SubDispatchInTransit
				}
			} else {
				entry.Status = SubDispatchInTransit
			}
		default:
			// Declined/withdrawn/expired with no active successor: falls back
			// to whatever entries[] already had it as (pending_assignment if
			// still owned by the aggregator, or left out if it moved on).
			if !exists {
				continue
			}
		}
		entries[jobID] = entry
	}

	result := make([]SubDispatchQueueEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func routeSummary(job domain.Job) string {
	pickup := job.Pickup.Address
	dropoff := job.Dropoff.Address
	if pickup == "" && dropoff == "" {
		return ""
	}
	return pickup + " → " + dropoff
}

// OperatorComplianceEntry is one linked operator's row on the "Compliance
// Snapshot" panel. KYC is omitted (see ComplianceStatusReader) — insurance and
// roadworthiness standing are folded into the same rate/counts fleet already
// computes for a tenant's own compliance documents screen.
type OperatorComplianceEntry struct {
	OperatorTenantID      string `json:"operator_tenant_id"`
	Total                 int    `json:"total"`
	Valid                 int    `json:"valid"`
	ExpiringSoon          int    `json:"expiring_soon"`
	Expired               int    `json:"expired"`
	ComplianceRatePercent int    `json:"compliance_rate_percent"`
}

// ComplianceSnapshotUseCase rolls up each linked operator's compliance
// standing, for the network overview's Compliance Snapshot panel.
type ComplianceSnapshotUseCase struct {
	relationships AggregatorRelationshipRepository
	compliance    ComplianceStatusReader
}

func NewComplianceSnapshotUseCase(
	relationships AggregatorRelationshipRepository,
	compliance ComplianceStatusReader,
) *ComplianceSnapshotUseCase {
	return &ComplianceSnapshotUseCase{relationships: relationships, compliance: compliance}
}

func (u *ComplianceSnapshotUseCase) Execute(ctx context.Context, aggregatorTenantID string) ([]OperatorComplianceEntry, error) {
	relationships, err := u.relationships.ListByAggregator(ctx, aggregatorTenantID)
	if err != nil {
		return nil, err
	}

	entries := make([]OperatorComplianceEntry, 0, len(relationships))
	for _, relationship := range relationships {
		if !relationship.IsActive() {
			continue
		}

		entry := OperatorComplianceEntry{OperatorTenantID: relationship.OperatorTenantID}
		if u.compliance != nil {
			if snapshot, err := u.compliance.Snapshot(ctx, relationship.OperatorTenantID); err == nil {
				entry.Total = snapshot.Total
				entry.Valid = snapshot.Valid
				entry.ExpiringSoon = snapshot.ExpiringSoon
				entry.Expired = snapshot.Expired
				entry.ComplianceRatePercent = snapshot.ComplianceRatePercent
			}
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

var (
	ErrSubDispatchRelationshipNotActive = errors.New("this operator is not an active member of your network")
	ErrSubDispatchJobNotFound           = errors.New("job not found")
)

// AssignSubDispatchJobUseCase is "Assign sub-dispatch job" — the aggregator
// picking a linked operator for a job sitting in Pending Assignment. It is a
// thin, validated wrapper over the existing job-offer machinery: the
// requirements are explicit that sub-dispatch is job offers again, one level
// up, and the offer mechanism should be built once.
type AssignSubDispatchJobUseCase struct {
	relationships AggregatorRelationshipRepository
	createOffer   *CreateJobOfferUseCase
	activity      AggregatorActivityRepository
	clock         Clock
	idGen         IDGenerator
	offerWindow   time.Duration
}

func NewAssignSubDispatchJobUseCase(
	relationships AggregatorRelationshipRepository,
	createOffer *CreateJobOfferUseCase,
	activity AggregatorActivityRepository,
	clock Clock,
	idGen IDGenerator,
) *AssignSubDispatchJobUseCase {
	return &AssignSubDispatchJobUseCase{
		relationships: relationships, createOffer: createOffer, activity: activity,
		clock: clock, idGen: idGen, offerWindow: 24 * time.Hour,
	}
}

type AssignSubDispatchJobCommand struct {
	AggregatorTenantID string
	JobID              string
	OperatorTenantID   string
	OfferedRateNgn     float64
}

func (u *AssignSubDispatchJobUseCase) Execute(ctx context.Context, command AssignSubDispatchJobCommand) (domain.JobOffer, error) {
	relationship, found, err := u.relationships.FindActiveBetween(ctx, command.AggregatorTenantID, command.OperatorTenantID)
	if err != nil {
		return domain.JobOffer{}, err
	}
	if !found || !relationship.IsActive() {
		return domain.JobOffer{}, ErrSubDispatchRelationshipNotActive
	}

	now := u.clock.Now()
	offer, err := u.createOffer.Execute(ctx, CreateJobOfferCommand{
		OfferingTenantID: command.AggregatorTenantID,
		JobID:            command.JobID,
		TargetTenantID:   command.OperatorTenantID,
		OfferedRateNgn:   command.OfferedRateNgn,
		ExpiresAt:        now.Add(u.offerWindow),
	})
	if err != nil {
		if errors.Is(err, ErrJobNotFound) {
			return domain.JobOffer{}, ErrSubDispatchJobNotFound
		}
		return domain.JobOffer{}, err
	}

	if u.activity != nil {
		entry, entryErr := domain.NewAggregatorActivityEntry(domain.AggregatorActivityEntry{
			ID:                 "act-" + u.idGen.New(),
			AggregatorTenantID: command.AggregatorTenantID,
			Type:               domain.ActivitySubDispatched,
			Description:        "Job " + command.JobID + " sub-dispatched to " + command.OperatorTenantID,
			RelatedEntityType:  "job_offer",
			RelatedEntityID:    offer.ID,
			OccurredAt:         now,
		})
		if entryErr == nil {
			_ = u.activity.Append(ctx, entry)
		}
	}

	return offer, nil
}
