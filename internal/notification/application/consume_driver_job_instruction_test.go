package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

type instructionTextRecorder struct {
	commands []SendMessageCommand
	err      error
}

func (s *instructionTextRecorder) Execute(_ context.Context, command SendMessageCommand) (domain.OutboxMessage, error) {
	s.commands = append(s.commands, command)
	return domain.OutboxMessage{}, s.err
}

type instructionTemplateRecorder struct {
	commands []SendJobInstructionCommand
	err      error
}

func (s *instructionTemplateRecorder) Execute(_ context.Context, command SendJobInstructionCommand) (domain.OutboxMessage, error) {
	s.commands = append(s.commands, command)
	return domain.OutboxMessage{}, s.err
}

type stubConversationFinder struct {
	conversation domain.WhatsAppConversation
	found        bool
	err          error
}

func (s stubConversationFinder) FindConversationByTenantAndDriverPhone(context.Context, string, string) (domain.WhatsAppConversation, bool, error) {
	return s.conversation, s.found, s.err
}

type stubAccountFinder struct {
	found bool
	err   error
}

func (s stubAccountFinder) FindAccountByTenantID(context.Context, string) (domain.WhatsAppAccount, bool, error) {
	return domain.WhatsAppAccount{}, s.found, s.err
}

type stubDriverLookup struct{ drivers []LinkedDriver }

func (s stubDriverLookup) DriversByPhone(context.Context, string) ([]LinkedDriver, error) {
	return s.drivers, nil
}

type instructionCharger struct {
	charges []UsageCharge
	err     error
}

func (c *instructionCharger) Charge(_ context.Context, charge UsageCharge) error {
	c.charges = append(c.charges, charge)
	return c.err
}

type instructionReporter struct{ reports []DriverInstructionReport }

func (r *instructionReporter) ReportDriverInstruction(_ context.Context, report DriverInstructionReport) error {
	r.reports = append(r.reports, report)
	return nil
}

type instructionHarness struct {
	text     *instructionTextRecorder
	template *instructionTemplateRecorder
	charger  *instructionCharger
	reporter *instructionReporter
	claims   *fakeEventClaims
	conv     stubConversationFinder
	account  stubAccountFinder
	useCase  *ConsumeDriverJobInstructionUseCase
}

var instructionNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func newInstructionHarness(account stubAccountFinder, conversation stubConversationFinder) *instructionHarness {
	h := &instructionHarness{
		text: &instructionTextRecorder{}, template: &instructionTemplateRecorder{}, charger: &instructionCharger{},
		reporter: &instructionReporter{}, claims: newFakeEventClaims(), conv: conversation, account: account,
	}
	h.useCase = NewConsumeDriverJobInstructionUseCase(DriverJobInstructionDeps{
		Text: h.text, Template: h.template, Conversations: conversation, Accounts: account,
		Drivers:  stubDriverLookup{drivers: []LinkedDriver{{DriverID: "driver-1", TenantID: "tenant-a", Name: "Chukwuma Okafor"}}},
		Charger:  h.charger,
		Reporter: h.reporter, Claims: h.claims, Clock: fixedNotificationClock{now: instructionNow},
		TemplateName: "job_assignment_v1", Language: "en", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return h
}

func instructionEvent(id string, overrides map[string]any) eventbus.EventEnvelope {
	data := map[string]any{
		"job_id": "job-1", "driver_id": "driver-1", "driver_phone": "+2348012345678",
		"job_reference": "TM-1", "pickup_location": "Apapa", "delivery_location": "Ikeja",
		"cargo_description": "Electronics", "priority": "high", "channel": "whatsapp",
		"message": "New job TM-1 assigned.",
	}
	for key, value := range overrides {
		data[key] = value
	}
	return eventbus.EventEnvelope{ID: id, Type: DriverJobInstructionEventType, TenantID: "tenant-a", Subject: "tm.tenant-a.driver.job.instruction", Data: data}
}

func openWindow() stubConversationFinder {
	expires := instructionNow.Add(3 * time.Hour)
	return stubConversationFinder{found: true, conversation: domain.WhatsAppConversation{WindowExpiresAt: &expires}}
}

func (h *instructionHarness) onlyReport(t *testing.T) DriverInstructionReport {
	t.Helper()
	if len(h.reporter.reports) != 1 {
		t.Fatalf("expected exactly one report, got %+v", h.reporter.reports)
	}
	return h.reporter.reports[0]
}

func TestDriverInstruction_InsideTheWindowSendsAPlainMessage(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: true}, openWindow())

	if err := h.useCase.Handle(context.Background(), instructionEvent("evt-1", nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(h.text.commands) != 1 || len(h.template.commands) != 0 {
		t.Fatalf("expected one text and no template, got text=%d template=%d", len(h.text.commands), len(h.template.commands))
	}
	sent := h.text.commands[0]
	if sent.TenantID != "tenant-a" || sent.Recipient != "+2348012345678" || sent.Content != "New job TM-1 assigned." || sent.MessageID != "msg-evt-1" {
		t.Fatalf("unexpected text command %+v", sent)
	}
	if report := h.onlyReport(t); report.Outcome != InstructionOutcomeSent || report.JobID != "job-1" || report.DriverID != "driver-1" || report.TenantID != "tenant-a" {
		t.Fatalf("unexpected report %+v", report)
	}
	if len(h.charger.charges) != 0 {
		t.Fatal("the text path is charged by the message sender, not here")
	}
}

func TestDriverInstruction_OutsideTheWindowSendsTheTemplateAndChargesOnce(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: true}, stubConversationFinder{})

	if err := h.useCase.Handle(context.Background(), instructionEvent("evt-2", nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(h.template.commands) != 1 || len(h.text.commands) != 0 {
		t.Fatalf("expected one template and no text, got template=%d text=%d", len(h.template.commands), len(h.text.commands))
	}
	sent := h.template.commands[0]
	if sent.TemplateName != "job_assignment_v1" || sent.DriverName != "Chukwuma Okafor" || sent.JobReference != "TM-1" ||
		sent.PickupLocation != "Apapa" || sent.DeliveryLocation != "Ikeja" || sent.CargoDescription != "Electronics" || sent.Priority != "high" || sent.Recipient != "+2348012345678" {
		t.Fatalf("unexpected template command %+v", sent)
	}
	if len(h.charger.charges) != 1 || h.charger.charges[0].TenantID != "tenant-a" || h.charger.charges[0].Channel != "whatsapp" || h.charger.charges[0].Reference != "msg-evt-2" {
		t.Fatalf("expected one whatsapp charge for the tenant, got %+v", h.charger.charges)
	}
	if h.onlyReport(t).Outcome != InstructionOutcomeSent {
		t.Fatalf("expected sent, got %+v", h.reporter.reports)
	}
}

func TestDriverInstruction_ExpiredWindowCountsAsOutside(t *testing.T) {
	expired := instructionNow.Add(-time.Minute)
	h := newInstructionHarness(stubAccountFinder{found: true}, stubConversationFinder{found: true, conversation: domain.WhatsAppConversation{WindowExpiresAt: &expired}})

	_ = h.useCase.Handle(context.Background(), instructionEvent("evt-3", nil))

	if len(h.template.commands) != 1 || len(h.text.commands) != 0 {
		t.Fatalf("expected the template, got template=%d text=%d", len(h.template.commands), len(h.text.commands))
	}
}

func TestDriverInstruction_TenantWithoutAWhatsAppNumberIsReportedNotSent(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: false}, openWindow())

	if err := h.useCase.Handle(context.Background(), instructionEvent("evt-4", nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	report := h.onlyReport(t)
	if report.Outcome != InstructionOutcomeNotSent || report.Reason != "no WhatsApp number" {
		t.Fatalf("expected not sent for no number, got %+v", report)
	}
	if len(h.text.commands)+len(h.template.commands)+len(h.charger.charges) != 0 {
		t.Fatal("nothing may be sent or charged without a number")
	}
}

func TestDriverInstruction_MissingPhoneIsReportedNotSent(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: true}, openWindow())

	_ = h.useCase.Handle(context.Background(), instructionEvent("evt-5", map[string]any{"driver_phone": ""}))

	if report := h.onlyReport(t); report.Outcome != InstructionOutcomeNotSent {
		t.Fatalf("expected not sent, got %+v", report)
	}
	if len(h.text.commands)+len(h.template.commands) != 0 {
		t.Fatal("nothing may be sent without a phone number")
	}
}

func TestDriverInstruction_ProviderFailureIsRecordedAndAcknowledged(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: true}, stubConversationFinder{})
	h.template.err = errors.New("job assignment template not found")

	if err := h.useCase.Handle(context.Background(), instructionEvent("evt-6", nil)); err != nil {
		t.Fatalf("a failed send is recorded, not retried: %v", err)
	}

	if report := h.onlyReport(t); report.Outcome != InstructionOutcomeFailed || report.Reason != "job assignment template not found" {
		t.Fatalf("expected failed with the reason, got %+v", report)
	}
}

func TestDriverInstruction_RefusedChargeIsReportedAsFailed(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: true}, stubConversationFinder{})
	h.charger.err = ErrInsufficientFunds

	_ = h.useCase.Handle(context.Background(), instructionEvent("evt-7", nil))

	if report := h.onlyReport(t); report.Outcome != InstructionOutcomeFailed || report.Reason != "wallet balance is too low" {
		t.Fatalf("expected a wallet refusal, got %+v", report)
	}
	if len(h.template.commands) != 0 {
		t.Fatal("a refused charge must not send")
	}
}

func TestDriverInstruction_ReplayedEventSendsOnce(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: true}, openWindow())

	_ = h.useCase.Handle(context.Background(), instructionEvent("evt-8", nil))
	_ = h.useCase.Handle(context.Background(), instructionEvent("evt-8", nil))

	if len(h.text.commands) != 1 || len(h.reporter.reports) != 1 {
		t.Fatalf("expected one send and one report, got text=%d reports=%d", len(h.text.commands), len(h.reporter.reports))
	}
}

func TestDriverInstruction_StorageFailureIsRetried(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{err: errors.New("db down")}, openWindow())

	if err := h.useCase.Handle(context.Background(), instructionEvent("evt-9", nil)); err == nil {
		t.Fatal("expected the error to be returned so the event is redelivered")
	}
	if len(h.reporter.reports) != 0 {
		t.Fatal("nothing is reported until the outcome is known")
	}

	h.useCase.accounts = stubAccountFinder{found: true}
	if err := h.useCase.Handle(context.Background(), instructionEvent("evt-9", nil)); err != nil || len(h.text.commands) != 1 {
		t.Fatalf("expected the redelivery to send, got err=%v sent=%d", err, len(h.text.commands))
	}
}

func TestDriverInstruction_IgnoresOtherEventsAndIncompleteOnes(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: true}, openWindow())

	other := instructionEvent("evt-10", nil)
	other.Type = "job.created"
	incomplete := instructionEvent("evt-11", map[string]any{"driver_id": ""})
	noID := instructionEvent("", nil)
	for _, envelope := range []eventbus.EventEnvelope{other, incomplete, noID} {
		if err := h.useCase.Handle(context.Background(), envelope); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if len(h.text.commands)+len(h.template.commands)+len(h.reporter.reports) != 0 {
		t.Fatal("nothing may be sent or reported for events that are not driver instructions")
	}
}

func TestDriverInstruction_UnknownDriverNameFallsBack(t *testing.T) {
	h := newInstructionHarness(stubAccountFinder{found: true}, stubConversationFinder{})
	h.useCase.drivers = stubDriverLookup{drivers: []LinkedDriver{{DriverID: "driver-1", TenantID: "tenant-other", Name: "Someone Else"}}}

	_ = h.useCase.Handle(context.Background(), instructionEvent("evt-12", nil))

	if got := h.template.commands[0].DriverName; got != "Driver" {
		t.Fatalf("a driver of another tenant must not be used, got %q", got)
	}
}
