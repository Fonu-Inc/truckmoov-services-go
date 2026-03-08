package infrastructure

import (
	"context"

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
