package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type outcomeRow struct {
	report    DriverInstructionReport
	status    string
	attempts  int
	next      time.Time
	claimedAt *time.Time
}

type fakeOutcomeOutbox struct {
	mu   sync.Mutex
	rows map[string]*outcomeRow
	fail error
}

func newFakeOutcomeOutbox() *fakeOutcomeOutbox {
	return &fakeOutcomeOutbox{rows: map[string]*outcomeRow{}}
}

func outcomeKey(report DriverInstructionReport) string {
	return report.TenantID + "|" + report.JobID + "|" + report.EventID
}

func (o *fakeOutcomeOutbox) EnsureSchema(context.Context) error { return nil }

func (o *fakeOutcomeOutbox) Enqueue(_ context.Context, report DriverInstructionReport, at time.Time) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		return false, o.fail
	}
	if _, exists := o.rows[outcomeKey(report)]; exists {
		return false, nil
	}
	o.rows[outcomeKey(report)] = &outcomeRow{report: report, status: "pending", next: at, claimedAt: &at}
	return true, nil
}

func (o *fakeOutcomeOutbox) MarkDelivered(_ context.Context, report DriverInstructionReport, _ time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	row := o.rows[outcomeKey(report)]
	row.status, row.claimedAt = "delivered", nil
	return nil
}

func (o *fakeOutcomeOutbox) Retry(_ context.Context, report DriverInstructionReport, attempts int, next time.Time, _ string, abandon bool, _ time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	row := o.rows[outcomeKey(report)]
	row.attempts, row.next, row.claimedAt = attempts, next, nil
	if abandon {
		row.status = "abandoned"
	}
	return nil
}

func (o *fakeOutcomeOutbox) ClaimDue(_ context.Context, now, claimedBefore time.Time, limit int) ([]PendingInstructionOutcome, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	claimed := []PendingInstructionOutcome{}
	for _, row := range o.rows {
		due := row.status == "pending" && !row.next.After(now) && (row.claimedAt == nil || row.claimedAt.Before(claimedBefore))
		if due && len(claimed) < limit {
			at := now
			row.claimedAt = &at
			claimed = append(claimed, PendingInstructionOutcome{Report: row.report, Attempts: row.attempts})
		}
	}
	return claimed, nil
}

type flakyDispatch struct {
	down  bool
	calls []DriverInstructionReport
}

func (d *flakyDispatch) ReportDriverInstruction(_ context.Context, report DriverInstructionReport) error {
	d.calls = append(d.calls, report)
	if d.down {
		return errors.New("dispatch service returned 503")
	}
	return nil
}

type movableClock struct{ now time.Time }

func (c *movableClock) Now() time.Time { return c.now }

type outcomeKit struct {
	outbox   *fakeOutcomeOutbox
	dispatch *flakyDispatch
	clock    *movableClock
	reporter *OutboxedInstructionReporter
	sweeper  *InstructionOutcomeSweeper
}

func newOutcomeKit() *outcomeKit {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	kit := &outcomeKit{outbox: newFakeOutcomeOutbox(), dispatch: &flakyDispatch{}, clock: &movableClock{now: instructionNow}}
	kit.reporter = NewOutboxedInstructionReporter(kit.outbox, kit.dispatch, kit.clock, log)
	kit.sweeper = NewInstructionOutcomeSweeper(kit.outbox, kit.dispatch, kit.clock, log)
	return kit
}

func sentReport(tenantID, jobID, eventID string) DriverInstructionReport {
	return DriverInstructionReport{TenantID: tenantID, JobID: jobID, EventID: eventID, DriverID: "driver-1", Outcome: InstructionOutcomeSent, Channel: "whatsapp"}
}

func TestInstructionOutcomeIsDeliveredAtOnceWhenDispatchIsUp(t *testing.T) {
	kit := newOutcomeKit()

	if err := kit.reporter.ReportDriverInstruction(context.Background(), sentReport("tenant-a", "job-1", "evt-1")); err != nil {
		t.Fatalf("report: %v", err)
	}

	if len(kit.dispatch.calls) != 1 || kit.outbox.rows["tenant-a|job-1|evt-1"].status != "delivered" {
		t.Fatalf("expected one delivered report, calls=%d status=%s", len(kit.dispatch.calls), kit.outbox.rows["tenant-a|job-1|evt-1"].status)
	}
}

func TestInstructionOutcomeIsRetriedUntilDispatchAcceptsIt(t *testing.T) {
	kit := newOutcomeKit()
	kit.dispatch.down = true
	report := sentReport("tenant-a", "job-1", "evt-1")

	if err := kit.reporter.ReportDriverInstruction(context.Background(), report); err != nil {
		t.Fatalf("a stored outcome is not an error: %v", err)
	}
	row := kit.outbox.rows[outcomeKey(report)]
	if row.status != "pending" || row.attempts != 1 {
		t.Fatalf("expected a pending row after one attempt, got %+v", row)
	}

	if delivered, _ := kit.sweeper.RunDue(context.Background()); delivered != 0 || len(kit.dispatch.calls) != 1 {
		t.Fatalf("nothing is due before the backoff: delivered=%d calls=%d", delivered, len(kit.dispatch.calls))
	}

	kit.clock.now = kit.clock.now.Add(time.Minute)
	kit.dispatch.down = false
	delivered, err := kit.sweeper.RunDue(context.Background())

	if err != nil || delivered != 1 || row.status != "delivered" {
		t.Fatalf("expected the sweep to deliver it: delivered=%d err=%v status=%s", delivered, err, row.status)
	}
	if again, _ := kit.sweeper.RunDue(context.Background()); again != 0 {
		t.Fatal("a delivered outcome must not be sent again")
	}
}

func TestInstructionOutcomeBackoffGrowsAndIsCapped(t *testing.T) {
	if got := instructionOutcomeBackoff(1); got != instructionOutcomeBackoffBase {
		t.Fatalf("first retry waits the base, got %s", got)
	}
	if instructionOutcomeBackoff(3) != 4*instructionOutcomeBackoffBase {
		t.Fatalf("expected doubling, got %s", instructionOutcomeBackoff(3))
	}
	if got := instructionOutcomeBackoff(40); got != instructionOutcomeBackoffCap {
		t.Fatalf("expected the cap, got %s", got)
	}
}

func TestInstructionOutcomeIsAbandonedAfterTooManyAttempts(t *testing.T) {
	kit := newOutcomeKit()
	kit.dispatch.down = true
	report := sentReport("tenant-a", "job-1", "evt-1")
	_ = kit.reporter.ReportDriverInstruction(context.Background(), report)

	for range instructionOutcomeMaxAttempts {
		kit.clock.now = kit.clock.now.Add(2 * time.Hour)
		_, _ = kit.sweeper.RunDue(context.Background())
	}

	if status := kit.outbox.rows[outcomeKey(report)].status; status != "abandoned" {
		t.Fatalf("expected the outcome to be given up on, got %s", status)
	}
}

func TestInstructionOutcomeRedeliveredEventIsNotReportedTwice(t *testing.T) {
	kit := newOutcomeKit()
	report := sentReport("tenant-a", "job-1", "evt-1")

	_ = kit.reporter.ReportDriverInstruction(context.Background(), report)
	_ = kit.reporter.ReportDriverInstruction(context.Background(), report)

	if len(kit.dispatch.calls) != 1 {
		t.Fatalf("expected one report to dispatch, got %d", len(kit.dispatch.calls))
	}
}

func TestInstructionOutcomesAreKeyedByTenantAndJob(t *testing.T) {
	kit := newOutcomeKit()

	_ = kit.reporter.ReportDriverInstruction(context.Background(), sentReport("tenant-a", "job-1", "evt-1"))
	_ = kit.reporter.ReportDriverInstruction(context.Background(), sentReport("tenant-b", "job-1", "evt-1"))

	if len(kit.outbox.rows) != 2 || len(kit.dispatch.calls) != 2 {
		t.Fatalf("two tenants with the same job id are two outcomes: rows=%d calls=%d", len(kit.outbox.rows), len(kit.dispatch.calls))
	}
	tenants := map[string]bool{}
	for _, call := range kit.dispatch.calls {
		tenants[call.TenantID] = true
	}
	if !tenants["tenant-a"] || !tenants["tenant-b"] {
		t.Fatalf("each outcome goes out under its own tenant: %+v", kit.dispatch.calls)
	}
}

func TestInstructionOutcomeFallsBackToADirectReportWhenTheStoreIsDown(t *testing.T) {
	kit := newOutcomeKit()
	kit.outbox.fail = errors.New("db down")

	if err := kit.reporter.ReportDriverInstruction(context.Background(), sentReport("tenant-a", "job-1", "evt-1")); err != nil {
		t.Fatalf("report: %v", err)
	}

	if len(kit.dispatch.calls) != 1 {
		t.Fatalf("expected one direct report, got %d", len(kit.dispatch.calls))
	}
}
