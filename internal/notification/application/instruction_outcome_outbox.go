package application

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	instructionOutcomeBatch       = 50
	instructionOutcomeMaxAttempts = 30
	instructionOutcomeClaimTTL    = 5 * time.Minute
	instructionOutcomeBackoffBase = 30 * time.Second
	instructionOutcomeBackoffCap  = time.Hour
)

// PendingInstructionOutcome is one outcome dispatch has not accepted yet. It is
// keyed by tenant, job and instruction event, never by job alone.
type PendingInstructionOutcome struct {
	Report   DriverInstructionReport
	Attempts int
}

// InstructionOutcomeOutbox keeps outcomes until dispatch accepts them, so a
// dispatch outage cannot leave a job timeline silent.
type InstructionOutcomeOutbox interface {
	EnsureSchema(ctx context.Context) error
	// Enqueue stores the outcome already claimed by the caller, so a sweep
	// does not pick it up while the first attempt is in flight. It reports
	// false when the same outcome was stored before, which is not sent twice.
	Enqueue(ctx context.Context, report DriverInstructionReport, at time.Time) (bool, error)
	MarkDelivered(ctx context.Context, report DriverInstructionReport, at time.Time) error
	// Retry puts a failed outcome back for a later sweep, or gives up on it.
	Retry(ctx context.Context, report DriverInstructionReport, attempts int, next time.Time, lastError string, abandon bool, at time.Time) error
	// ClaimDue atomically takes outcomes that are due and not held by a live
	// claim, so replicas never send the same one together.
	ClaimDue(ctx context.Context, now, claimedBefore time.Time, limit int) ([]PendingInstructionOutcome, error)
}

type instructionOutcomeSender struct {
	outbox   InstructionOutcomeOutbox
	reporter DriverInstructionReporter
	clock    Clock
	log      *slog.Logger
}

func (s instructionOutcomeSender) attempt(ctx context.Context, pending PendingInstructionOutcome) error {
	now := s.clock.Now()
	err := s.reporter.ReportDriverInstruction(ctx, pending.Report)
	if err == nil {
		return s.outbox.MarkDelivered(ctx, pending.Report, now)
	}
	attempts := pending.Attempts + 1
	abandon := attempts >= instructionOutcomeMaxAttempts
	if abandon {
		s.log.Error("driver_instruction_outcome_abandoned", "tenant", pending.Report.TenantID, "job", pending.Report.JobID, "attempts", attempts, "error", err)
	}
	return errors.Join(err, s.outbox.Retry(ctx, pending.Report, attempts, now.Add(instructionOutcomeBackoff(attempts)), err.Error(), abandon, now))
}

func instructionOutcomeBackoff(attempts int) time.Duration {
	delay := instructionOutcomeBackoffBase
	for i := 1; i < attempts && delay < instructionOutcomeBackoffCap; i++ {
		delay *= 2
	}
	return min(delay, instructionOutcomeBackoffCap)
}

// OutboxedInstructionReporter stores every outcome before it tries dispatch,
// so one that dispatch cannot take now is retried by the sweep.
type OutboxedInstructionReporter struct {
	direct DriverInstructionReporter
	sender instructionOutcomeSender
}

func NewOutboxedInstructionReporter(outbox InstructionOutcomeOutbox, direct DriverInstructionReporter, clock Clock, log *slog.Logger) *OutboxedInstructionReporter {
	return &OutboxedInstructionReporter{direct: direct, sender: instructionOutcomeSender{outbox: outbox, reporter: direct, clock: clock, log: log}}
}

// ReportDriverInstruction returns nil once the outcome is stored: delivery
// from then on is the sweep's job. Only a store that is down falls back to a
// single direct attempt.
func (r *OutboxedInstructionReporter) ReportDriverInstruction(ctx context.Context, report DriverInstructionReport) error {
	created, err := r.sender.outbox.Enqueue(ctx, report, r.sender.clock.Now())
	if err != nil {
		r.sender.log.Error("driver_instruction_outcome_enqueue_failed", "tenant", report.TenantID, "job", report.JobID, "error", err)
		return r.direct.ReportDriverInstruction(ctx, report)
	}
	if !created {
		return nil
	}
	if err := r.sender.attempt(ctx, PendingInstructionOutcome{Report: report}); err != nil {
		r.sender.log.Warn("driver_instruction_outcome_deferred", "tenant", report.TenantID, "job", report.JobID, "error", err)
	}
	return nil
}

// InstructionOutcomeSweeper retries stored outcomes with backoff until
// dispatch accepts them.
type InstructionOutcomeSweeper struct {
	sender instructionOutcomeSender
}

func NewInstructionOutcomeSweeper(outbox InstructionOutcomeOutbox, reporter DriverInstructionReporter, clock Clock, log *slog.Logger) *InstructionOutcomeSweeper {
	return &InstructionOutcomeSweeper{sender: instructionOutcomeSender{outbox: outbox, reporter: reporter, clock: clock, log: log}}
}

// RunDue returns how many outcomes dispatch accepted in this pass.
func (s *InstructionOutcomeSweeper) RunDue(ctx context.Context) (int, error) {
	now := s.sender.clock.Now()
	due, err := s.sender.outbox.ClaimDue(ctx, now, now.Add(-instructionOutcomeClaimTTL), instructionOutcomeBatch)
	if err != nil {
		return 0, err
	}
	delivered := 0
	var failures error
	for _, pending := range due {
		if err := s.sender.attempt(ctx, pending); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		delivered++
	}
	return delivered, failures
}
