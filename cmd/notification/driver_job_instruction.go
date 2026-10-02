package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

const (
	driverInstructionSubject   = "tm.*.driver.job.instruction"
	driverInstructionConsumer  = "driver-job-instruction"
	defaultInstructionTemplate = "job_assignment_v1"
)

type driverJobInstructionWiring struct {
	bus      eventbus.EventBus
	verified func(eventbus.Handler) eventbus.Handler
	log      *slog.Logger
	deps     application.DriverJobInstructionDeps
}

// wireDriverJobInstruction starts messaging a driver when dispatch assigns
// them a job. The template name comes from JOB_INSTRUCTION_TEMPLATE.
func wireDriverJobInstruction(ctx context.Context, wiring driverJobInstructionWiring) {
	deps := wiring.deps
	deps.TemplateName = instructionTemplateName()
	deps.Log = wiring.log
	consumer := application.NewConsumeDriverJobInstructionUseCase(deps)
	if _, err := wiring.bus.Subscribe(ctx, driverInstructionSubject, driverInstructionConsumer, wiring.verified(consumer.Handle)); err != nil {
		wiring.log.Error("driver_job_instruction_subscribe_failed", "error", err.Error())
	}
}

func instructionTemplateName() string {
	if name := strings.TrimSpace(os.Getenv("JOB_INSTRUCTION_TEMPLATE")); name != "" {
		return name
	}
	return defaultInstructionTemplate
}
