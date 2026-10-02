package infrastructure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

type DriverInstructionNotifier struct {
	bus eventbus.EventBus
}

func NewDriverInstructionNotifier(bus eventbus.EventBus) *DriverInstructionNotifier {
	return &DriverInstructionNotifier{bus: bus}
}

// NotifyJobInstruction publishes the request for the notification service to
// message the driver. It carries everything the message needs, so the
// consumer does not have to call back for the job.
func (n *DriverInstructionNotifier) NotifyJobInstruction(ctx context.Context, job domain.Job, message string) error {
	tenantID := strings.TrimSpace(job.TenantID)
	if tenantID == "" || strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.AssignedDriverID) == "" || strings.TrimSpace(message) == "" {
		return fmt.Errorf("tenant_id, job_id, driver_id and message are required")
	}

	return n.bus.Publish(ctx, fmt.Sprintf("tm.%s.driver.job.instruction", tenantID), eventbus.EventEnvelope{
		ID:         fmt.Sprintf("driver-instruction-%d", time.Now().UnixNano()),
		Type:       "driver.job.instruction",
		Version:    1,
		OccurredAt: time.Now().UTC(),
		TenantID:   tenantID,
		Data: map[string]any{
			"job_id":            job.ID,
			"driver_id":         job.AssignedDriverID,
			"driver_phone":      job.AssignedDriverPhone,
			"job_reference":     firstNonBlank(job.Reference, job.ID),
			"pickup_location":   job.Pickup.Address,
			"delivery_location": job.Dropoff.Address,
			"cargo_description": instructionCargo(job),
			"priority":          string(job.Priority),
			"channel":           "whatsapp",
			"message":           strings.TrimSpace(message),
		},
	})
}

func instructionCargo(job domain.Job) string {
	if description := strings.TrimSpace(job.CargoDescription); description != "" {
		return description
	}
	if cargoType := strings.TrimSpace(job.CargoType); cargoType != "" {
		return cargoType
	}
	if job.LoadWeightKg > 0 {
		return fmt.Sprintf("%.0fkg load", job.LoadWeightKg)
	}
	return "General cargo"
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
