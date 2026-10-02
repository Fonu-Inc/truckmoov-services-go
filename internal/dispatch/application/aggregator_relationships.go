package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

var (
	ErrRelationshipNotFound       = errors.New("relationship not found")
	ErrRelationshipAlreadyExists  = errors.New("this operator is already linked to your network")
	ErrRelationshipNotYours       = errors.New("this relationship does not belong to your organisation")
	ErrOperatorHasActiveJobs      = errors.New("operator has active jobs from this network and cannot be removed")
	ErrInvitedEmailMismatch       = errors.New("this invitation was not sent to your account")
	ErrOperatorIdentifierRequired = errors.New("either an existing operator or an email to invite is required")
)

type AddOperatorCommand struct {
	AggregatorTenantID string
	// Exactly one of these is set — the client already knows whether it
	// picked an existing organisation or is inviting a new one.
	OperatorTenantID string
	InvitedEmail     string
	Types            []domain.RelationshipType
	FeeRatePct       float64
	Regions          []domain.Region
}

// AddOperatorUseCase links an owner-operator into an aggregator's network.
type AddOperatorUseCase struct {
	relationships AggregatorRelationshipRepository
	activity      AggregatorActivityRepository
	clock         Clock
	idGen         IDGenerator
}

func NewAddOperatorUseCase(
	relationships AggregatorRelationshipRepository,
	activity AggregatorActivityRepository,
	clock Clock,
	idGen IDGenerator,
) *AddOperatorUseCase {
	return &AddOperatorUseCase{relationships: relationships, activity: activity, clock: clock, idGen: idGen}
}

func (u *AddOperatorUseCase) Execute(ctx context.Context, command AddOperatorCommand) (domain.AggregatorRelationship, error) {
	if command.OperatorTenantID == "" && command.InvitedEmail == "" {
		return domain.AggregatorRelationship{}, ErrOperatorIdentifierRequired
	}

	if command.OperatorTenantID != "" {
		existing, found, err := u.relationships.FindActiveBetween(ctx, command.AggregatorTenantID, command.OperatorTenantID)
		if err != nil {
			return domain.AggregatorRelationship{}, fmt.Errorf("check existing relationship: %w", err)
		}
		if found && existing.Status != domain.RelationshipStatusEnded {
			return domain.AggregatorRelationship{}, ErrRelationshipAlreadyExists
		}
	}

	now := u.clock.Now()
	relationship, err := domain.NewAggregatorRelationship(domain.AggregatorRelationship{
		ID:                 "aggrel-" + u.idGen.New(),
		AggregatorTenantID: command.AggregatorTenantID,
		OperatorTenantID:   command.OperatorTenantID,
		InvitedEmail:       command.InvitedEmail,
		Types:              command.Types,
		FeeRatePct:         command.FeeRatePct,
		Regions:            command.Regions,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		return domain.AggregatorRelationship{}, err
	}

	saved, err := u.relationships.Create(ctx, relationship)
	if err != nil {
		return domain.AggregatorRelationship{}, fmt.Errorf("save relationship: %w", err)
	}

	if saved.Status == domain.RelationshipStatusActive {
		u.recordOnboarded(ctx, saved, now)
	}

	return saved, nil
}

func (u *AddOperatorUseCase) recordOnboarded(ctx context.Context, relationship domain.AggregatorRelationship, now time.Time) {
	if u.activity == nil {
		return
	}
	entry, err := domain.NewAggregatorActivityEntry(domain.AggregatorActivityEntry{
		ID:                 "act-" + u.idGen.New(),
		AggregatorTenantID: relationship.AggregatorTenantID,
		Type:               domain.ActivityOperatorOnboarded,
		Description:        "Operator " + relationship.OperatorTenantID + " onboarded to the network",
		RelatedEntityType:  "aggregator_relationship",
		RelatedEntityID:    relationship.ID,
		OccurredAt:         now,
	})
	if err != nil {
		return
	}
	_ = u.activity.Append(ctx, entry)
}

// AcceptAggregatorInviteUseCase turns a pending invitation into an active
// relationship once the invited organisation registers and confirms.
type AcceptAggregatorInviteUseCase struct {
	relationships AggregatorRelationshipRepository
	activity      AggregatorActivityRepository
	clock         Clock
	idGen         IDGenerator
}

func NewAcceptAggregatorInviteUseCase(
	relationships AggregatorRelationshipRepository,
	activity AggregatorActivityRepository,
	clock Clock,
	idGen IDGenerator,
) *AcceptAggregatorInviteUseCase {
	return &AcceptAggregatorInviteUseCase{relationships: relationships, activity: activity, clock: clock, idGen: idGen}
}

type AcceptAggregatorInviteCommand struct {
	RelationshipID        string
	AcceptingTenantID     string
	AcceptingAccountEmail string
}

func (u *AcceptAggregatorInviteUseCase) Execute(ctx context.Context, command AcceptAggregatorInviteCommand) (domain.AggregatorRelationship, error) {
	relationship, found, err := u.relationships.FindByID(ctx, command.RelationshipID)
	if err != nil {
		return domain.AggregatorRelationship{}, err
	}
	if !found {
		return domain.AggregatorRelationship{}, ErrRelationshipNotFound
	}
	if !equalFoldTrim(relationship.InvitedEmail, command.AcceptingAccountEmail) {
		return domain.AggregatorRelationship{}, ErrInvitedEmailMismatch
	}

	now := u.clock.Now()
	accepted, err := relationship.AcceptInvite(command.AcceptingTenantID, now)
	if err != nil {
		return domain.AggregatorRelationship{}, err
	}
	if err := u.relationships.Save(ctx, accepted); err != nil {
		return domain.AggregatorRelationship{}, fmt.Errorf("save accepted relationship: %w", err)
	}

	if u.activity != nil {
		entry, err := domain.NewAggregatorActivityEntry(domain.AggregatorActivityEntry{
			ID:                 "act-" + u.idGen.New(),
			AggregatorTenantID: accepted.AggregatorTenantID,
			Type:               domain.ActivityOperatorOnboarded,
			Description:        "Operator " + accepted.OperatorTenantID + " accepted the network invitation",
			RelatedEntityType:  "aggregator_relationship",
			RelatedEntityID:    accepted.ID,
			OccurredAt:         now,
		})
		if err == nil {
			_ = u.activity.Append(ctx, entry)
		}
	}

	return accepted, nil
}

// RemoveOperatorUseCase ends a relationship. Refused while the operator
// still has active jobs sub-dispatched from this aggregator — money and
// cargo mid-flight cannot simply be orphaned by a network change.
type RemoveOperatorUseCase struct {
	relationships AggregatorRelationshipRepository
	offers        JobOfferRepository
	jobs          JobRepository
	clock         Clock
}

func NewRemoveOperatorUseCase(
	relationships AggregatorRelationshipRepository,
	offers JobOfferRepository,
	jobs JobRepository,
	clock Clock,
) *RemoveOperatorUseCase {
	return &RemoveOperatorUseCase{relationships: relationships, offers: offers, jobs: jobs, clock: clock}
}

type RemoveOperatorCommand struct {
	AggregatorTenantID string
	RelationshipID     string
	Reason             string
}

func (u *RemoveOperatorUseCase) Execute(ctx context.Context, command RemoveOperatorCommand) (domain.AggregatorRelationship, error) {
	relationship, found, err := u.relationships.FindByID(ctx, command.RelationshipID)
	if err != nil {
		return domain.AggregatorRelationship{}, err
	}
	if !found {
		return domain.AggregatorRelationship{}, ErrRelationshipNotFound
	}
	if relationship.AggregatorTenantID != command.AggregatorTenantID {
		return domain.AggregatorRelationship{}, ErrRelationshipNotYours
	}

	hasActive, err := u.hasActiveSubDispatchedJobs(ctx, relationship)
	if err != nil {
		return domain.AggregatorRelationship{}, err
	}
	if hasActive {
		return domain.AggregatorRelationship{}, ErrOperatorHasActiveJobs
	}

	ended, err := relationship.End(command.Reason, u.clock.Now())
	if err != nil {
		return domain.AggregatorRelationship{}, err
	}
	if err := u.relationships.Save(ctx, ended); err != nil {
		return domain.AggregatorRelationship{}, fmt.Errorf("save ended relationship: %w", err)
	}
	return ended, nil
}

func (u *RemoveOperatorUseCase) hasActiveSubDispatchedJobs(ctx context.Context, relationship domain.AggregatorRelationship) (bool, error) {
	if relationship.OperatorTenantID == "" {
		return false, nil
	}
	accepted, err := u.offers.ListAccepted(ctx, relationship.AggregatorTenantID, relationship.OperatorTenantID)
	if err != nil {
		return false, fmt.Errorf("list accepted sub-dispatch offers: %w", err)
	}
	for _, offer := range accepted {
		job, err := offeredJob(u.jobs, offer)
		if errors.Is(err, ErrJobNotFound) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("load offered job: %w", err)
		}
		if job.IsActive() {
			return true, nil
		}
	}
	return false, nil
}

// ListLinkedOperatorsUseCase serves the "Linked Owner-Operators" cards: one
// per active relationship, with that operator's capacity at a glance.
type ListLinkedOperatorsUseCase struct {
	relationships AggregatorRelationshipRepository
	jobs          JobRepository
	fleetCounts   FleetCountsReader
	ratings       OperatorRatingRepository
}

func NewListLinkedOperatorsUseCase(
	relationships AggregatorRelationshipRepository,
	jobs JobRepository,
	fleetCounts FleetCountsReader,
	ratings OperatorRatingRepository,
) *ListLinkedOperatorsUseCase {
	return &ListLinkedOperatorsUseCase{relationships: relationships, jobs: jobs, fleetCounts: fleetCounts, ratings: ratings}
}

type LinkedOperator struct {
	Relationship domain.AggregatorRelationship `json:"relationship"`
	VehicleCount int                           `json:"vehicle_count"`
	DriverCount  int                           `json:"driver_count"`
	ActiveJobs   int                           `json:"active_job_count"`
	Rating       float64                       `json:"rating"`
	RatingCount  int                           `json:"rating_count"`
}

func (u *ListLinkedOperatorsUseCase) Execute(ctx context.Context, aggregatorTenantID string) ([]LinkedOperator, error) {
	relationships, err := u.relationships.ListByAggregator(ctx, aggregatorTenantID)
	if err != nil {
		return nil, err
	}

	operators := make([]LinkedOperator, 0, len(relationships))
	for _, relationship := range relationships {
		if !relationship.IsActive() {
			continue
		}
		operator, err := u.snapshot(ctx, relationship)
		if err != nil {
			return nil, err
		}
		operators = append(operators, operator)
	}
	return operators, nil
}

func (u *ListLinkedOperatorsUseCase) snapshot(ctx context.Context, relationship domain.AggregatorRelationship) (LinkedOperator, error) {
	result := LinkedOperator{Relationship: relationship}

	if u.fleetCounts != nil {
		if vehicles, drivers, err := u.fleetCounts.TenantCounts(ctx, relationship.OperatorTenantID); err == nil {
			result.VehicleCount = vehicles
			result.DriverCount = drivers
		}
	}

	if u.jobs != nil {
		operatorJobs, err := u.jobs.FindByTenant(relationship.OperatorTenantID)
		if err != nil {
			return LinkedOperator{}, fmt.Errorf("list operator jobs: %w", err)
		}
		for _, job := range operatorJobs {
			if job.IsActive() {
				result.ActiveJobs++
			}
		}
	}

	if u.ratings != nil {
		if avg, count, err := u.ratings.AverageForOperator(ctx, relationship.OperatorTenantID); err == nil {
			result.Rating = avg
			result.RatingCount = count
		}
	}

	return result, nil
}

func equalFoldTrim(a, b string) bool {
	normalizedA := strings.ToLower(strings.TrimSpace(a))
	normalizedB := strings.ToLower(strings.TrimSpace(b))
	return normalizedA != "" && normalizedA == normalizedB
}
