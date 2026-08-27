package domain

import (
	"errors"
	"strings"
	"time"
)

// AggregatorActivityEntry is one row of the Live Network Activity feed —
// written directly by whatever use case caused it, rather than assembled
// from a separate event stream.
type AggregatorActivityType string

const (
	ActivityOperatorOnboarded AggregatorActivityType = "operator_onboarded"
	ActivitySubDispatched     AggregatorActivityType = "sub_dispatched"
	ActivitySettlementPaid    AggregatorActivityType = "settlement_paid"
)

type AggregatorActivityEntry struct {
	ID                 string
	AggregatorTenantID string
	Type               AggregatorActivityType
	// Description is built once, server-side, from the structured fields
	// below — never the only record of what happened, so it can be relinked
	// or relocalised without re-deriving it from a sentence.
	Description       string
	RelatedEntityType string
	RelatedEntityID   string
	OccurredAt        time.Time
}

func NewAggregatorActivityEntry(params AggregatorActivityEntry) (AggregatorActivityEntry, error) {
	if strings.TrimSpace(params.ID) == "" {
		return AggregatorActivityEntry{}, errors.New("activity entry id is required")
	}
	if strings.TrimSpace(params.AggregatorTenantID) == "" {
		return AggregatorActivityEntry{}, errors.New("aggregator tenant id is required")
	}
	if strings.TrimSpace(string(params.Type)) == "" {
		return AggregatorActivityEntry{}, errors.New("activity type is required")
	}
	if strings.TrimSpace(params.Description) == "" {
		return AggregatorActivityEntry{}, errors.New("description is required")
	}
	if params.OccurredAt.IsZero() {
		return AggregatorActivityEntry{}, errors.New("occurred_at is required")
	}

	return AggregatorActivityEntry{
		ID:                 strings.TrimSpace(params.ID),
		AggregatorTenantID: strings.TrimSpace(params.AggregatorTenantID),
		Type:               params.Type,
		Description:        strings.TrimSpace(params.Description),
		RelatedEntityType:  strings.TrimSpace(params.RelatedEntityType),
		RelatedEntityID:    strings.TrimSpace(params.RelatedEntityID),
		OccurredAt:         params.OccurredAt.UTC(),
	}, nil
}
