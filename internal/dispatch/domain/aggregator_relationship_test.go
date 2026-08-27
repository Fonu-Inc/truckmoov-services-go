package domain

import (
	"testing"
	"time"
)

func validRelationshipFixture(t *testing.T) AggregatorRelationship {
	t.Helper()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	rel, err := NewAggregatorRelationship(AggregatorRelationship{
		ID:                 "rel-1",
		AggregatorTenantID: "aggregator-1",
		OperatorTenantID:   "operator-1",
		Types:              []RelationshipType{RelationshipOwnerOperator},
		FeeRatePct:         12.5,
		Regions:            []Region{RegionLagos, RegionIbadan},
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("unexpected error building fixture: %v", err)
	}
	return rel
}

func TestNewAggregatorRelationship_RequiresOperatorOrInvite(t *testing.T) {
	now := time.Now()
	_, err := NewAggregatorRelationship(AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1",
		Types:     []RelationshipType{RelationshipOwnerOperator},
		CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error when neither operator tenant nor invited email is set")
	}
}

func TestNewAggregatorRelationship_CannotLinkSelf(t *testing.T) {
	now := time.Now()
	_, err := NewAggregatorRelationship(AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "tenant-1", OperatorTenantID: "tenant-1",
		Types:     []RelationshipType{RelationshipOwnerOperator},
		CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error when aggregator and operator tenant are the same")
	}
}

func TestNewAggregatorRelationship_InviteYieldsPendingStatus(t *testing.T) {
	now := time.Now()
	rel, err := NewAggregatorRelationship(AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", InvitedEmail: "new-operator@example.com",
		Types:     []RelationshipType{RelationshipOwnerOperator},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.Status != RelationshipStatusPending {
		t.Fatalf("expected pending status, got %q", rel.Status)
	}
}

func TestNewAggregatorRelationship_ExistingOperatorYieldsActiveStatus(t *testing.T) {
	rel := validRelationshipFixture(t)
	if rel.Status != RelationshipStatusActive {
		t.Fatalf("expected active status, got %q", rel.Status)
	}
}

func TestNewAggregatorRelationship_RejectsUnknownType(t *testing.T) {
	now := time.Now()
	_, err := NewAggregatorRelationship(AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types:     []RelationshipType{"not_a_real_type"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error for unknown relationship type")
	}
}

func TestNewAggregatorRelationship_RejectsFeeRateOutOfRange(t *testing.T) {
	now := time.Now()
	_, err := NewAggregatorRelationship(AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []RelationshipType{RelationshipOwnerOperator}, FeeRatePct: 150,
		CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error for fee rate over 100")
	}
}

func TestNewAggregatorRelationship_RejectsUnknownRegion(t *testing.T) {
	now := time.Now()
	_, err := NewAggregatorRelationship(AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types: []RelationshipType{RelationshipOwnerOperator}, Regions: []Region{"mars"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("expected error for unknown region")
	}
}

func TestNewAggregatorRelationship_DedupesTypesAndRegions(t *testing.T) {
	now := time.Now()
	rel, err := NewAggregatorRelationship(AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", OperatorTenantID: "operator-1",
		Types:     []RelationshipType{RelationshipOwnerOperator, RelationshipOwnerOperator},
		Regions:   []Region{RegionLagos, RegionLagos},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rel.Types) != 1 {
		t.Fatalf("expected deduped types, got %v", rel.Types)
	}
	if len(rel.Regions) != 1 {
		t.Fatalf("expected deduped regions, got %v", rel.Regions)
	}
}

func TestAggregatorRelationship_AcceptInvite(t *testing.T) {
	now := time.Now()
	rel, err := NewAggregatorRelationship(AggregatorRelationship{
		ID: "rel-1", AggregatorTenantID: "aggregator-1", InvitedEmail: "op@example.com",
		Types:     []RelationshipType{RelationshipOwnerOperator},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	accepted, err := rel.AcceptInvite("operator-9", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error accepting invite: %v", err)
	}
	if accepted.Status != RelationshipStatusActive {
		t.Fatalf("expected active after accept, got %q", accepted.Status)
	}
	if accepted.OperatorTenantID != "operator-9" {
		t.Fatalf("expected operator tenant id set, got %q", accepted.OperatorTenantID)
	}
	if accepted.InvitedEmail != "" {
		t.Fatalf("expected invited email cleared, got %q", accepted.InvitedEmail)
	}
}

func TestAggregatorRelationship_AcceptInvite_RefusedWhenNotPending(t *testing.T) {
	rel := validRelationshipFixture(t)
	if _, err := rel.AcceptInvite("operator-9", time.Now()); err == nil {
		t.Fatal("expected error accepting invite on an already-active relationship")
	}
}

func TestAggregatorRelationship_End(t *testing.T) {
	rel := validRelationshipFixture(t)
	ended, err := rel.End("operator requested exit", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ended.Status != RelationshipStatusEnded {
		t.Fatalf("expected ended status, got %q", ended.Status)
	}
	if ended.EndedAt == nil {
		t.Fatal("expected EndedAt to be set")
	}
	if ended.IsActive() {
		t.Fatal("expected ended relationship to report IsActive() false")
	}
}

func TestAggregatorRelationship_End_RefusedWhenAlreadyEnded(t *testing.T) {
	rel := validRelationshipFixture(t)
	ended, err := rel.End("x", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := ended.End("x", time.Now()); err == nil {
		t.Fatal("expected error ending an already-ended relationship")
	}
}

func TestAggregatorRelationship_HasType(t *testing.T) {
	rel := validRelationshipFixture(t)
	if !rel.HasType(RelationshipOwnerOperator) {
		t.Fatal("expected HasType true for a type the relationship holds")
	}
	if rel.HasType(RelationshipAggregatedFleet) {
		t.Fatal("expected HasType false for a type the relationship does not hold")
	}
}
