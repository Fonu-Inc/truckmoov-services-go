package activitylog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

type PublishCommand struct {
	TenantID   string
	Service    string
	Action     string
	EntityType string
	EntityID   string
	Summary    string
	ActorID    string
	ActorRole  string
	Metadata   map[string]any
	OccurredAt time.Time
}

type Publisher struct {
	bus eventbus.EventBus
}

func NewPublisher(bus eventbus.EventBus) *Publisher {
	return &Publisher{bus: bus}
}

func (p *Publisher) Publish(ctx context.Context, command PublishCommand) error {
	occurredAt := command.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	envelope := eventbus.EventEnvelope{
		ID:         newEventID(),
		Type:       "activity.logged",
		Version:    1,
		OccurredAt: occurredAt,
		TenantID:   command.TenantID,
		Data: map[string]any{
			"service":     command.Service,
			"action":      command.Action,
			"entity_type": command.EntityType,
			"entity_id":   command.EntityID,
			"summary":     command.Summary,
			"metadata":    cloneMetadata(command.Metadata),
		},
	}

	if command.ActorID != "" || command.ActorRole != "" {
		envelope.Actor = map[string]any{}
		if command.ActorID != "" {
			envelope.Actor["id"] = command.ActorID
		}
		if command.ActorRole != "" {
			envelope.Actor["role"] = command.ActorRole
		}
	}

	subject := fmt.Sprintf("tm.%s.activity.logged", command.TenantID)
	return p.bus.Publish(ctx, subject, envelope)
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}

	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func newEventID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("activity-%d", time.Now().UTC().UnixNano())
	}
	return "activity-" + hex.EncodeToString(buffer)
}
