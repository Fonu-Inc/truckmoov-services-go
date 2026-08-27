package domain

import (
	"errors"
	"strings"
	"time"
)

// An aggregator does not own trucks. It holds a relationship with an
// owner-operator organisation and passes work to them. This is that
// relationship record — the thing that grants an aggregator visibility into
// an operator's capacity and performance, and nothing wider.
type RelationshipType string

const (
	RelationshipOwnerOperator   RelationshipType = "owner_operator"
	RelationshipAggregatedFleet RelationshipType = "aggregated_fleet"
)

func isKnownRelationshipType(t RelationshipType) bool {
	return t == RelationshipOwnerOperator || t == RelationshipAggregatedFleet
}

type RelationshipStatus string

const (
	// Pending: an invitation sent to an operator not yet on the platform.
	RelationshipStatusPending RelationshipStatus = "pending"
	RelationshipStatusActive  RelationshipStatus = "active"
	// Ended: relationship closed. Kept, not deleted — settlements and past
	// sub-dispatched jobs still reference it.
	RelationshipStatusEnded RelationshipStatus = "ended"
)

// Region is a fixed reference list, not derived from free-text addresses —
// the reference build's donut chart has a fixed slice list, and a fuzzy match
// against job addresses would be the wrong foundation for it.
type Region string

const (
	RegionLagos        Region = "lagos"
	RegionAbuja        Region = "abuja"
	RegionPortHarcourt Region = "port_harcourt"
	RegionKano         Region = "kano"
	RegionIbadan       Region = "ibadan"
	RegionKaduna       Region = "kaduna"
	RegionOther        Region = "other"
)

var knownRegions = map[Region]bool{
	RegionLagos: true, RegionAbuja: true, RegionPortHarcourt: true,
	RegionKano: true, RegionIbadan: true, RegionKaduna: true, RegionOther: true,
}

func IsKnownRegion(r Region) bool {
	return knownRegions[r]
}

type AggregatorRelationship struct {
	ID                 string
	AggregatorTenantID string
	// Empty until an invited (not-yet-registered) operator accepts.
	OperatorTenantID string
	// Set only while OperatorTenantID is empty — the address the invite went to.
	InvitedEmail string
	Types        []RelationshipType
	// Percentage, 0-100. Drives the settlement calculation, so it lives here
	// rather than as a global constant: two operators can negotiate differently.
	FeeRatePct  float64
	Regions     []Region
	Status      RelationshipStatus
	EndedAt     *time.Time
	EndedReason string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func NewAggregatorRelationship(params AggregatorRelationship) (AggregatorRelationship, error) {
	if strings.TrimSpace(params.ID) == "" {
		return AggregatorRelationship{}, errors.New("relationship id is required")
	}
	if strings.TrimSpace(params.AggregatorTenantID) == "" {
		return AggregatorRelationship{}, errors.New("aggregator tenant id is required")
	}
	operatorTenantID := strings.TrimSpace(params.OperatorTenantID)
	invitedEmail := strings.TrimSpace(params.InvitedEmail)
	if operatorTenantID == "" && invitedEmail == "" {
		return AggregatorRelationship{}, errors.New("either an operator tenant id or an invited email is required")
	}
	if operatorTenantID != "" && operatorTenantID == strings.TrimSpace(params.AggregatorTenantID) {
		return AggregatorRelationship{}, errors.New("an aggregator cannot link itself as an operator")
	}
	if len(params.Types) == 0 {
		return AggregatorRelationship{}, errors.New("at least one relationship type is required")
	}
	types := make([]RelationshipType, 0, len(params.Types))
	seenTypes := map[RelationshipType]bool{}
	for _, t := range params.Types {
		if !isKnownRelationshipType(t) {
			return AggregatorRelationship{}, errors.New("unknown relationship type")
		}
		if seenTypes[t] {
			continue
		}
		seenTypes[t] = true
		types = append(types, t)
	}
	if params.FeeRatePct < 0 || params.FeeRatePct > 100 {
		return AggregatorRelationship{}, errors.New("fee rate must be between 0 and 100")
	}
	regions := make([]Region, 0, len(params.Regions))
	seenRegions := map[Region]bool{}
	for _, r := range params.Regions {
		if !IsKnownRegion(r) {
			return AggregatorRelationship{}, errors.New("unknown region")
		}
		if seenRegions[r] {
			continue
		}
		seenRegions[r] = true
		regions = append(regions, r)
	}
	if params.CreatedAt.IsZero() || params.UpdatedAt.IsZero() {
		return AggregatorRelationship{}, errors.New("created_at and updated_at are required")
	}

	status := params.Status
	if status == "" {
		if operatorTenantID == "" {
			status = RelationshipStatusPending
		} else {
			status = RelationshipStatusActive
		}
	}

	return AggregatorRelationship{
		ID:                 strings.TrimSpace(params.ID),
		AggregatorTenantID: strings.TrimSpace(params.AggregatorTenantID),
		OperatorTenantID:   operatorTenantID,
		InvitedEmail:       invitedEmail,
		Types:              types,
		FeeRatePct:         params.FeeRatePct,
		Regions:            regions,
		Status:             status,
		CreatedAt:          params.CreatedAt.UTC(),
		UpdatedAt:          params.UpdatedAt.UTC(),
	}, nil
}

// AcceptInvite is what turns a pending invitation into an active
// relationship once the invited organisation registers.
func (a AggregatorRelationship) AcceptInvite(operatorTenantID string, now time.Time) (AggregatorRelationship, error) {
	operatorTenantID = strings.TrimSpace(operatorTenantID)
	if operatorTenantID == "" {
		return AggregatorRelationship{}, errors.New("operator tenant id is required")
	}
	if a.Status != RelationshipStatusPending {
		return AggregatorRelationship{}, errors.New("only a pending invitation can be accepted")
	}

	updated := a
	updated.OperatorTenantID = operatorTenantID
	updated.InvitedEmail = ""
	updated.Status = RelationshipStatusActive
	updated.UpdatedAt = now.UTC()
	return updated, nil
}

// End closes the relationship. Whether the operator has active jobs is a
// cross-aggregate fact the caller must have already checked — this method
// only enforces the state machine, not that precondition.
func (a AggregatorRelationship) End(reason string, now time.Time) (AggregatorRelationship, error) {
	reason = strings.TrimSpace(reason)
	if a.Status == RelationshipStatusEnded {
		return AggregatorRelationship{}, errors.New("relationship has already ended")
	}

	updated := a
	updated.Status = RelationshipStatusEnded
	updated.EndedReason = reason
	endedAt := now.UTC()
	updated.EndedAt = &endedAt
	updated.UpdatedAt = endedAt
	return updated, nil
}

func (a AggregatorRelationship) IsActive() bool {
	return a.Status == RelationshipStatusActive
}

func (a AggregatorRelationship) HasType(t RelationshipType) bool {
	for _, existing := range a.Types {
		if existing == t {
			return true
		}
	}
	return false
}
