package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

var (
	ErrSettlementNotFound = errors.New("settlement not found")
	ErrSettlementNotYours = errors.New("this settlement does not belong to your organisation")
)

// WeeklySettlementCloseUseCase closes the most recently finished Mon-Sun UTC
// week for every active relationship, one settlement per aggregator-operator
// pair, computed from that week's delivered sub-dispatched jobs. Idempotent:
// a period already closed for a pair is skipped, so a daily tick (simpler and
// safer than trying to fire exactly once a week off a bare ticker) never
// double-creates.
type WeeklySettlementCloseUseCase struct {
	relationships AggregatorRelationshipRepository
	offers        JobOfferRepository
	jobs          JobRepository
	settlements   SettlementRepository
	clock         Clock
	idGen         IDGenerator
}

func NewWeeklySettlementCloseUseCase(
	relationships AggregatorRelationshipRepository,
	offers JobOfferRepository,
	jobs JobRepository,
	settlements SettlementRepository,
	clock Clock,
	idGen IDGenerator,
) *WeeklySettlementCloseUseCase {
	return &WeeklySettlementCloseUseCase{
		relationships: relationships, offers: offers, jobs: jobs,
		settlements: settlements, clock: clock, idGen: idGen,
	}
}

// Execute closes settlements across every tenant that has ever linked an
// operator.
func (u *WeeklySettlementCloseUseCase) Execute(ctx context.Context) (int, error) {
	now := u.clock.Now()
	periodStart, periodEnd := previousWeekWindow(now)
	if !periodEnd.Before(now) {
		// The previous week has not fully elapsed yet relative to the clock
		// under test; nothing to close.
		return 0, nil
	}

	aggregatorTenantIDs, err := u.relationships.ListDistinctAggregatorTenants(ctx)
	if err != nil {
		return 0, fmt.Errorf("list aggregator tenants: %w", err)
	}

	closed := 0
	for _, aggregatorTenantID := range aggregatorTenantIDs {
		relationships, err := u.relationships.ListByAggregator(ctx, aggregatorTenantID)
		if err != nil {
			return closed, fmt.Errorf("list relationships for %s: %w", aggregatorTenantID, err)
		}

		for _, relationship := range relationships {
			if !relationship.IsActive() {
				continue
			}

			if _, found, err := u.settlements.FindForPeriod(ctx, aggregatorTenantID, relationship.OperatorTenantID, periodStart); err != nil {
				return closed, fmt.Errorf("check existing settlement: %w", err)
			} else if found {
				continue
			}

			gross, err := u.grossEarningsForPeriod(ctx, aggregatorTenantID, relationship.OperatorTenantID, periodStart, periodEnd)
			if err != nil {
				return closed, err
			}
			if gross <= 0 {
				continue
			}

			settlement, err := domain.NewSettlement(domain.Settlement{
				ID:                 "settlement-" + u.idGen.New(),
				AggregatorTenantID: aggregatorTenantID,
				OperatorTenantID:   relationship.OperatorTenantID,
				RelationshipID:     relationship.ID,
				PeriodStart:        periodStart,
				PeriodEnd:          periodEnd,
				GrossEarningsNgn:   gross,
				FeeRatePctApplied:  relationship.FeeRatePct,
				CreatedAt:          now,
				UpdatedAt:          now,
			})
			if err != nil {
				return closed, fmt.Errorf("build settlement: %w", err)
			}
			if _, err := u.settlements.Create(ctx, settlement); err != nil {
				return closed, fmt.Errorf("save settlement: %w", err)
			}
			closed++
		}
	}
	return closed, nil
}

func (u *WeeklySettlementCloseUseCase) grossEarningsForPeriod(
	ctx context.Context, aggregatorTenantID, operatorTenantID string, periodStart, periodEnd time.Time,
) (float64, error) {
	accepted, err := u.offers.ListAccepted(ctx, aggregatorTenantID, operatorTenantID)
	if err != nil {
		return 0, fmt.Errorf("list accepted offers: %w", err)
	}

	var gross float64
	for _, offer := range accepted {
		job, found := offeredJob(u.jobs, offer)
		if !found || job.Status != domain.StatusDelivered {
			continue
		}
		deliveredAt := deliveredTimestamp(job)
		if deliveredAt == nil || deliveredAt.Before(periodStart) || !deliveredAt.Before(periodEnd) {
			continue
		}
		gross += offer.OfferedRateNgn
	}
	return gross, nil
}

// previousWeekWindow returns [start, end) for the Mon-Sun UTC week before
// the one `now` falls in.
func previousWeekWindow(now time.Time) (time.Time, time.Time) {
	nowUTC := now.UTC()
	weekday := int(nowUTC.Weekday())
	if weekday == 0 {
		weekday = 7 // ISO: Sunday is day 7, not 0.
	}
	thisWeekStart := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -(weekday - 1))
	previousWeekStart := thisWeekStart.AddDate(0, 0, -7)
	return previousWeekStart, thisWeekStart
}

// ListSettlementsUseCase serves the "Settlements & Payouts" table.
type ListSettlementsUseCase struct {
	settlements SettlementRepository
}

func NewListSettlementsUseCase(settlements SettlementRepository) *ListSettlementsUseCase {
	return &ListSettlementsUseCase{settlements: settlements}
}

type ListSettlementsResult struct {
	Settlements []domain.Settlement `json:"settlements"`
	Total       int                 `json:"total"`
}

func (u *ListSettlementsUseCase) Execute(ctx context.Context, aggregatorTenantID string, limit, offset int) (ListSettlementsResult, error) {
	settlements, total, err := u.settlements.ListByAggregator(ctx, aggregatorTenantID, limit, offset)
	if err != nil {
		return ListSettlementsResult{}, err
	}
	return ListSettlementsResult{Settlements: settlements, Total: total}, nil
}

// PaySettlementUseCase records that a pending settlement has been paid.
type PaySettlementUseCase struct {
	settlements SettlementRepository
	activity    AggregatorActivityRepository
	clock       Clock
	idGen       IDGenerator
}

func NewPaySettlementUseCase(
	settlements SettlementRepository,
	activity AggregatorActivityRepository,
	clock Clock,
	idGen IDGenerator,
) *PaySettlementUseCase {
	return &PaySettlementUseCase{settlements: settlements, activity: activity, clock: clock, idGen: idGen}
}

type PaySettlementCommand struct {
	AggregatorTenantID string
	SettlementID       string
	PaymentReference   string
	PaidBy             string
}

func (u *PaySettlementUseCase) Execute(ctx context.Context, command PaySettlementCommand) (domain.Settlement, error) {
	settlement, found, err := u.settlements.FindByID(ctx, command.SettlementID)
	if err != nil {
		return domain.Settlement{}, err
	}
	if !found {
		return domain.Settlement{}, ErrSettlementNotFound
	}
	if settlement.AggregatorTenantID != command.AggregatorTenantID {
		return domain.Settlement{}, ErrSettlementNotYours
	}

	now := u.clock.Now()
	paid, err := settlement.Pay(command.PaymentReference, command.PaidBy, now)
	if err != nil {
		return domain.Settlement{}, err
	}
	if err := u.settlements.Save(ctx, paid); err != nil {
		return domain.Settlement{}, fmt.Errorf("save paid settlement: %w", err)
	}

	if u.activity != nil {
		entry, entryErr := domain.NewAggregatorActivityEntry(domain.AggregatorActivityEntry{
			ID:                 "act-" + u.idGen.New(),
			AggregatorTenantID: command.AggregatorTenantID,
			Type:               domain.ActivitySettlementPaid,
			Description:        fmt.Sprintf("Settlement of ₦%.2f paid to %s", paid.NetPayoutNgn, paid.OperatorTenantID),
			RelatedEntityType:  "settlement",
			RelatedEntityID:    paid.ID,
			OccurredAt:         now,
		})
		if entryErr == nil {
			_ = u.activity.Append(ctx, entry)
		}
	}

	return paid, nil
}
