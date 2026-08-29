package infrastructure

import (
	"context"

	"github.com/truckmoov/services-go/internal/tracking/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

type EventPublisher struct {
	bus eventbus.EventBus
}

func NewEventPublisher(bus eventbus.EventBus) *EventPublisher {
	return &EventPublisher{bus: bus}
}

func (p *EventPublisher) PublishLocationUpdate(ctx context.Context, event domain.LocationUpdate) error {
	return p.bus.Publish(ctx, event.Subject(), eventbus.EventEnvelope{
		ID:         event.EventID,
		Type:       "vehicle.location.updated",
		Version:    1,
		OccurredAt: event.OccurredAt,
		TenantID:   event.TenantID,
		Data: map[string]any{
			"vehicle_id": event.VehicleID,
			"device_id":  event.DeviceID,
			"imei":       event.IMEI,
			"lat":        event.Latitude,
			"lng":        event.Longitude,
			"speed_kph":  event.SpeedKPH,
			"heading":    event.Heading,
			"accuracy_m": event.AccuracyM,
			"fix_time":   event.FixTimeRFC,
			"protocol":   event.Protocol,
		},
	})
}

func (p *EventPublisher) PublishAlertTriggered(ctx context.Context, event domain.AlertTriggered) error {
	return p.bus.Publish(ctx, event.Subject(), eventbus.EventEnvelope{
		ID:         event.EventID,
		Type:       "alert.triggered",
		Version:    1,
		OccurredAt: event.OccurredAt,
		TenantID:   event.TenantID,
		Data: map[string]any{
			"alert_id":   event.AlertID,
			"alert_type": event.AlertType,
			"severity":   event.Severity,
			"vehicle_id": event.VehicleID,
			"job_id":     event.JobID,
			"details":    event.Details,
		},
	})
}
