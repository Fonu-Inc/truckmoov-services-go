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

func (p *EventPublisher) locationEnvelope(event domain.LocationUpdate) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{
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
	}
}

type beginPublisher interface {
	BeginPublish(ctx context.Context, subject string, envelope eventbus.EventEnvelope) (func() error, error)
}

type beginCorePublisher interface {
	BeginPublishCore(ctx context.Context, subject string, envelope eventbus.EventEnvelope) (func() error, error)
}

func (p *EventPublisher) PublishLocationUpdate(ctx context.Context, event domain.LocationUpdate) error {
	ack, err := p.BeginLocationUpdate(ctx, event)
	if err != nil {
		return err
	}
	return ack()
}

// BeginLocationUpdate starts a core-NATS publish (Flush ack) so Redis work can
// overlap; JetStream is best-effort async for durable consumers.
func (p *EventPublisher) BeginLocationUpdate(ctx context.Context, event domain.LocationUpdate) (func() error, error) {
	envelope := p.locationEnvelope(event)
	if bp, ok := p.bus.(beginCorePublisher); ok {
		return bp.BeginPublishCore(ctx, event.Subject(), envelope)
	}
	if bp, ok := p.bus.(beginPublisher); ok {
		return bp.BeginPublish(ctx, event.Subject(), envelope)
	}
	if err := p.bus.Publish(ctx, event.Subject(), envelope); err != nil {
		return nil, err
	}
	return func() error { return nil }, nil
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

func (p *EventPublisher) PublishRoadMatched(ctx context.Context, event domain.RoadMatched) error {
	return p.bus.Publish(ctx, event.Subject(), eventbus.EventEnvelope{
		ID:         event.EventID,
		Type:       "vehicle.road.matched",
		Version:    1,
		OccurredAt: event.OccurredAt,
		TenantID:   event.TenantID,
		Data: map[string]any{
			"vehicle_id":          event.VehicleID,
			"device_id":           event.DeviceID,
			"fix_time":            event.FixTimeRFC,
			"lat":                 event.Latitude,
			"lng":                 event.Longitude,
			"speed_kph":           event.SpeedKPH,
			"road_class":          event.RoadClass,
			"road_name":           event.RoadName,
			"osm_speed_limit_kph": event.OSMSpeedLimitKPH,
			"way_id":              event.WayID,
		},
	})
}
