package infrastructure

import (
	"context"
	"fmt"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

type AlertEventPublisher struct {
	bus         eventbus.EventBus
	clock       SystemClock
	idGenerator TimeIDGenerator
}

func NewAlertEventPublisher(bus eventbus.EventBus) *AlertEventPublisher {
	return &AlertEventPublisher{bus: bus}
}

func (p *AlertEventPublisher) PublishAlert(ctx context.Context, tenantID, alertType, severity, jobID, vehicleID string, details map[string]any) error {
	subject := fmt.Sprintf("tm.%s.alert.triggered", tenantID)
	return p.bus.Publish(ctx, subject, eventbus.EventEnvelope{
		ID:         p.idGenerator.New(),
		Type:       "alert.triggered",
		Version:    1,
		OccurredAt: p.clock.Now(),
		TenantID:   tenantID,
		Data: map[string]any{
			"alert_type": alertType,
			"severity":   severity,
			"job_id":     jobID,
			"vehicle_id": vehicleID,
			"details":    details,
		},
	})
}
