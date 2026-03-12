package infrastructure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

type DriverInstructionNotifier struct {
	bus eventbus.EventBus
}

func NewDriverInstructionNotifier(bus eventbus.EventBus) *DriverInstructionNotifier {
	return &DriverInstructionNotifier{bus: bus}
}

func (n *DriverInstructionNotifier) NotifyJobInstruction(
	ctx context.Context,
	tenantID, jobID, driverID, message string,
) error {
	tenantID = strings.TrimSpace(tenantID)
	jobID = strings.TrimSpace(jobID)
	driverID = strings.TrimSpace(driverID)
	message = strings.TrimSpace(message)
	if tenantID == "" || jobID == "" || driverID == "" || message == "" {
		return fmt.Errorf("tenant_id, job_id, driver_id and message are required")
	}

	subject := fmt.Sprintf("tm.%s.driver.job.instruction", tenantID)
	// Dispatch publishes an instruction event; notification context can consume and send WhatsApp.
	return n.bus.Publish(ctx, subject, eventbus.EventEnvelope{
		ID:         fmt.Sprintf("driver-instruction-%d", time.Now().UnixNano()),
		Type:       "driver.job.instruction",
		Version:    1,
		OccurredAt: time.Now().UTC(),
		TenantID:   tenantID,
		Data: map[string]any{
			"job_id":    jobID,
			"driver_id": driverID,
			"channel":   "whatsapp",
			"message":   message,
		},
	})
}
