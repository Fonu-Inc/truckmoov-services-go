package infrastructure

import (
	"context"
	"encoding/json"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

type DeliveryEventPublisher struct {
	bus eventbus.EventBus
}

func NewDeliveryEventPublisher(bus eventbus.EventBus) *DeliveryEventPublisher {
	return &DeliveryEventPublisher{bus: bus}
}

func (p *DeliveryEventPublisher) PublishDeliveryUpdated(
	ctx context.Context,
	event domain.MessageDeliveryUpdated,
) error {
	payload := map[string]any{
		"message_id": event.MessageID,
		"channel":    event.Channel,
		"status":     event.Status,
	}
	if event.ErrorText != "" {
		payload["error"] = map[string]any{"message": event.ErrorText}
	}

	return p.bus.Publish(ctx, event.Subject(), eventbus.EventEnvelope{
		ID:         event.EventID,
		Type:       "message.delivery.updated",
		Version:    1,
		OccurredAt: event.OccurredAt,
		TenantID:   event.TenantID,
		Data:       payload,
	})
}

const platformMessagingTenant = "system"

// PlatformMessagingEventPublisher sends inbox events for the realtime gateway.
// The bus signs every envelope it publishes; the gateway verifies before it
// forwards anything to staff.
type PlatformMessagingEventPublisher struct {
	bus   eventbus.EventBus
	clock application.Clock
	ids   application.IDGenerator
}

func NewPlatformMessagingEventPublisher(bus eventbus.EventBus, clock application.Clock, ids application.IDGenerator) *PlatformMessagingEventPublisher {
	return &PlatformMessagingEventPublisher{bus: bus, clock: clock, ids: ids}
}

func (p *PlatformMessagingEventPublisher) PublishMessageCreated(ctx context.Context, payload application.MessageCreatedView) error {
	return p.publish(ctx, "message.created", payload)
}

func (p *PlatformMessagingEventPublisher) PublishMessageStatus(ctx context.Context, payload application.MessageStatusView) error {
	return p.publish(ctx, "message.status", payload)
}

func (p *PlatformMessagingEventPublisher) PublishConversationUpdated(ctx context.Context, payload application.ConversationUpdatedView) error {
	return p.publish(ctx, "conversation.updated", payload)
}

func (p *PlatformMessagingEventPublisher) publish(ctx context.Context, name string, payload any) error {
	data, err := toEventData(payload)
	if err != nil {
		return err
	}
	return p.bus.Publish(ctx, "tm.system.messaging."+name, eventbus.EventEnvelope{
		ID:         p.ids.New("evt"),
		Type:       "messaging." + name,
		Version:    1,
		OccurredAt: p.clock.Now().UTC(),
		TenantID:   platformMessagingTenant,
		Data:       data,
	})
}

// toEventData goes through JSON so the envelope carries exactly the wire
// shape, with only strings, booleans, null and whole numbers where possible.
func toEventData(payload any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	return data, nil
}
