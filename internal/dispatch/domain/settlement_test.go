package domain

import (
	"testing"
	"time"
)

func validSettlementFixture(t *testing.T, gross, feeRate float64) Settlement {
	t.Helper()
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	settlement, err := NewSettlement(Settlement{
		ID: "settlement-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		RelationshipID: "rel-1", PeriodStart: now, PeriodEnd: now.AddDate(0, 0, 7),
		GrossEarningsNgn: gross, FeeRatePctApplied: feeRate,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected error building fixture: %v", err)
	}
	return settlement
}

func TestNewSettlement_ComputesFeeAndNet(t *testing.T) {
	settlement := validSettlementFixture(t, 100000, 12.5)
	if settlement.AggregatorFeeNgn != 12500 {
		t.Fatalf("expected fee 12500, got %v", settlement.AggregatorFeeNgn)
	}
	if settlement.NetPayoutNgn != 87500 {
		t.Fatalf("expected net 87500, got %v", settlement.NetPayoutNgn)
	}
	if settlement.Status != SettlementStatusPending {
		t.Fatalf("expected pending status, got %q", settlement.Status)
	}
}

func TestNewSettlement_RoundsToKobo(t *testing.T) {
	// 33333.33 * 10% = 3333.333 -> rounds to 3333.33
	settlement := validSettlementFixture(t, 33333.33, 10)
	if settlement.AggregatorFeeNgn != 3333.33 {
		t.Fatalf("expected fee rounded to 3333.33, got %v", settlement.AggregatorFeeNgn)
	}
	if settlement.NetPayoutNgn != 30000.00 {
		t.Fatalf("expected net rounded to 30000.00, got %v", settlement.NetPayoutNgn)
	}
}

func TestNewSettlement_RejectsBackwardsPeriod(t *testing.T) {
	now := time.Now()
	_, err := NewSettlement(Settlement{
		ID: "settlement-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		RelationshipID: "rel-1", PeriodStart: now, PeriodEnd: now.Add(-time.Hour),
		GrossEarningsNgn: 1000, FeeRatePctApplied: 10,
		CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error for period end before period start")
	}
}

func TestNewSettlement_RejectsNegativeGross(t *testing.T) {
	now := time.Now()
	_, err := NewSettlement(Settlement{
		ID: "settlement-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		RelationshipID: "rel-1", PeriodStart: now, PeriodEnd: now.Add(time.Hour),
		GrossEarningsNgn: -1, FeeRatePctApplied: 10,
		CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error for negative gross earnings")
	}
}

func TestSettlement_Pay(t *testing.T) {
	settlement := validSettlementFixture(t, 100000, 10)
	paid, err := settlement.Pay("PAY-REF-123", "admin-1", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paid.Status != SettlementStatusPaid {
		t.Fatalf("expected paid status, got %q", paid.Status)
	}
	if paid.PaymentReference != "PAY-REF-123" {
		t.Fatalf("expected payment reference stored, got %q", paid.PaymentReference)
	}
	if paid.PaidAt == nil {
		t.Fatal("expected PaidAt to be set")
	}
	// Money figures must not move on payment.
	if paid.AggregatorFeeNgn != settlement.AggregatorFeeNgn || paid.NetPayoutNgn != settlement.NetPayoutNgn {
		t.Fatal("expected fee/net to stay fixed across payment")
	}
}

func TestSettlement_Pay_RequiresReference(t *testing.T) {
	settlement := validSettlementFixture(t, 100000, 10)
	if _, err := settlement.Pay("", "admin-1", time.Now()); err == nil {
		t.Fatal("expected error for missing payment reference")
	}
}

func TestSettlement_Pay_RefusedWhenAlreadyPaid(t *testing.T) {
	settlement := validSettlementFixture(t, 100000, 10)
	paid, err := settlement.Pay("PAY-REF-1", "admin-1", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := paid.Pay("PAY-REF-2", "admin-1", time.Now()); err == nil {
		t.Fatal("expected error paying an already-paid settlement")
	}
}
