package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

const (
	driverInstructionSubject   = "tm.*.driver.job.instruction"
	driverInstructionConsumer  = "driver-job-instruction"
	defaultInstructionTemplate = "job_assignment_v1"
	instructionOutcomeInterval = 30 * time.Second
)

type driverJobInstructionWiring struct {
	bus      eventbus.EventBus
	verified func(eventbus.Handler) eventbus.Handler
	log      *slog.Logger
	deps     application.DriverJobInstructionDeps
	outcomes application.InstructionOutcomeOutbox
}

// wireDriverJobInstruction starts messaging a driver when dispatch assigns
// them a job. The template name comes from JOB_INSTRUCTION_TEMPLATE.
func wireDriverJobInstruction(ctx context.Context, wiring driverJobInstructionWiring) {
	deps := wiring.deps
	deps.TemplateName = instructionTemplateName()
	deps.Log = wiring.log
	if wiring.outcomes != nil {
		sweeper := application.NewInstructionOutcomeSweeper(wiring.outcomes, deps.Reporter, deps.Clock, wiring.log)
		deps.Reporter = application.NewOutboxedInstructionReporter(wiring.outcomes, deps.Reporter, deps.Clock, wiring.log)
		go runInstructionOutcomeSweep(ctx, sweeper, wiring.log)
	}
	consumer := application.NewConsumeDriverJobInstructionUseCase(deps)
	if _, err := wiring.bus.Subscribe(ctx, driverInstructionSubject, driverInstructionConsumer, wiring.verified(consumer.Handle)); err != nil {
		wiring.log.Error("driver_job_instruction_subscribe_failed", "error", err.Error())
	}
}

// runInstructionOutcomeSweep keeps offering stored outcomes to dispatch until
// it accepts them.
func runInstructionOutcomeSweep(ctx context.Context, sweeper *application.InstructionOutcomeSweeper, log *slog.Logger) {
	ticker := time.NewTicker(instructionOutcomeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := sweeper.RunDue(ctx); err != nil {
				log.Warn("driver_instruction_outcome_sweep_failed", "error", err.Error())
			}
		}
	}
}

func instructionTemplateName() string {
	if name := strings.TrimSpace(os.Getenv("JOB_INSTRUCTION_TEMPLATE")); name != "" {
		return name
	}
	return defaultInstructionTemplate
}

// instructionOutcomeOutbox keeps a nil repository a nil interface, which
// leaves outcomes reported directly when the table could not be created.
func instructionOutcomeOutbox(repository *infrastructure.PostgresInstructionOutcomeRepository) application.InstructionOutcomeOutbox {
	if repository == nil {
		return nil
	}
	return repository
}
