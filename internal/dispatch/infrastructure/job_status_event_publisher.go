package infrastructure

import (
	"context"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

type JobStatusEventPublisher struct {
	bus eventbus.EventBus
}

func NewJobStatusEventPublisher(bus eventbus.EventBus) *JobStatusEventPublisher {
	return &JobStatusEventPublisher{bus: bus}
}

func (p *JobStatusEventPublisher) PublishJobStatusChanged(
	ctx context.Context,
	event domain.JobStatusChanged,
) error {
	envelope := eventbus.EventEnvelope{
		ID:         event.EventID,
		Type:       "job.status.changed",
		Version:    1,
		OccurredAt: event.OccurredAt,
		TenantID:   event.TenantID,
		Data: map[string]any{
			"job_id":              event.JobID,
			"previous_status":     event.PreviousStatus,
			"new_status":          event.NewStatus,
			"assigned_driver_id":  event.AssignedDriverID,
			"assigned_vehicle_id": event.AssignedVehicleID,
			"reason":              event.Reason,
		},
	}

	return p.bus.Publish(ctx, event.Subject(), envelope)
}
