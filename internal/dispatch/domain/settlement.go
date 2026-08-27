package domain

import (
	"errors"
	"math"
	"strings"
	"time"
)

// Settlement is one aggregator-operator pair's money for one week. Money
// moving between organisations needs an audit trail, not a status field: the
// gross/fee/net figures are fixed at creation and never recomputed, even if
// the relationship's fee rate changes afterward — a settlement is what was
// actually calculated, not what the current rate would produce.
type SettlementStatus string

const (
	SettlementStatusPending SettlementStatus = "pending"
	SettlementStatusPaid    SettlementStatus = "paid"
)

type Settlement struct {
	ID                 string
	AggregatorTenantID string
	OperatorTenantID   string
	RelationshipID     string
	PeriodStart        time.Time
	PeriodEnd          time.Time
	GrossEarningsNgn   float64
	// The relationship's fee rate at the moment this was calculated, kept
	// alongside the money it produced so the figure can be explained later
	// even if the relationship's rate has since changed.
	FeeRatePctApplied float64
	AggregatorFeeNgn  float64
	NetPayoutNgn      float64
	Status            SettlementStatus
	PaymentReference  string
	PaidAt            *time.Time
	PaidBy            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewSettlement computes the fee and net payout from the gross and the
// relationship's fee rate. Rounded to the kobo (2dp): Naira has no smaller
// unit, and carrying float error into a payout figure is worse than a
// half-kobo rounding.
func NewSettlement(params Settlement) (Settlement, error) {
	if strings.TrimSpace(params.ID) == "" {
		return Settlement{}, errors.New("settlement id is required")
	}
	if strings.TrimSpace(params.AggregatorTenantID) == "" {
		return Settlement{}, errors.New("aggregator tenant id is required")
	}
	if strings.TrimSpace(params.OperatorTenantID) == "" {
		return Settlement{}, errors.New("operator tenant id is required")
	}
	if strings.TrimSpace(params.RelationshipID) == "" {
		return Settlement{}, errors.New("relationship id is required")
	}
	if params.PeriodStart.IsZero() || params.PeriodEnd.IsZero() {
		return Settlement{}, errors.New("period start and end are required")
	}
	if !params.PeriodStart.Before(params.PeriodEnd) {
		return Settlement{}, errors.New("period start must be before period end")
	}
	if params.GrossEarningsNgn < 0 {
		return Settlement{}, errors.New("gross earnings cannot be negative")
	}
	if params.FeeRatePctApplied < 0 || params.FeeRatePctApplied > 100 {
		return Settlement{}, errors.New("fee rate must be between 0 and 100")
	}
	if params.CreatedAt.IsZero() || params.UpdatedAt.IsZero() {
		return Settlement{}, errors.New("created_at and updated_at are required")
	}

	fee := roundToKobo(params.GrossEarningsNgn * params.FeeRatePctApplied / 100)
	net := roundToKobo(params.GrossEarningsNgn - fee)

	return Settlement{
		ID:                 strings.TrimSpace(params.ID),
		AggregatorTenantID: strings.TrimSpace(params.AggregatorTenantID),
		OperatorTenantID:   strings.TrimSpace(params.OperatorTenantID),
		RelationshipID:     strings.TrimSpace(params.RelationshipID),
		PeriodStart:        params.PeriodStart.UTC(),
		PeriodEnd:          params.PeriodEnd.UTC(),
		GrossEarningsNgn:   params.GrossEarningsNgn,
		FeeRatePctApplied:  params.FeeRatePctApplied,
		AggregatorFeeNgn:   fee,
		NetPayoutNgn:       net,
		Status:             SettlementStatusPending,
		CreatedAt:          params.CreatedAt.UTC(),
		UpdatedAt:          params.UpdatedAt.UTC(),
	}, nil
}

func roundToKobo(value float64) float64 {
	return math.Round(value*100) / 100
}

// Pay marks a pending settlement paid. Once paid it does not move again —
// there is no unpay — matching the requirement that this is a real state,
// not a flag that can be flipped back.
func (s Settlement) Pay(paymentReference, paidBy string, now time.Time) (Settlement, error) {
	paymentReference = strings.TrimSpace(paymentReference)
	if paymentReference == "" {
		return Settlement{}, errors.New("a payment reference is required")
	}
	if s.Status == SettlementStatusPaid {
		return Settlement{}, errors.New("this settlement has already been paid")
	}

	updated := s
	updated.Status = SettlementStatusPaid
	updated.PaymentReference = paymentReference
	updated.PaidBy = strings.TrimSpace(paidBy)
	paidAt := now.UTC()
	updated.PaidAt = &paidAt
	updated.UpdatedAt = paidAt
	return updated, nil
}
