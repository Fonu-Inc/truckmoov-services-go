package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/identity"
)

const (
	DriverJobInstructionEventType = "driver.job.instruction"

	InstructionOutcomeSent    = "sent"
	InstructionOutcomeFailed  = "failed"
	InstructionOutcomeNotSent = "not_sent"

	defaultInstructionDriverName = "Driver"
)

// DriverInstructionReport is what happened to one driver instruction.
type DriverInstructionReport struct {
	TenantID string
	JobID    string
	DriverID string
	Outcome  string
	Channel  string
	Reason   string
}

// DriverInstructionReporter tells dispatch what happened, so the job timeline
// only ever says what is true.
type DriverInstructionReporter interface {
	ReportDriverInstruction(ctx context.Context, report DriverInstructionReport) error
}

type instructionTemplateSender interface {
	Execute(ctx context.Context, command SendJobInstructionCommand) (domain.OutboxMessage, error)
}

type instructionTextSender interface {
	Execute(ctx context.Context, command SendMessageCommand) (domain.OutboxMessage, error)
}

type instructionConversationFinder interface {
	FindConversationByTenantAndDriverPhone(ctx context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error)
}

type instructionAccountFinder interface {
	FindAccountByTenantID(ctx context.Context, tenantID string) (domain.WhatsAppAccount, bool, error)
}

type ConsumeDriverJobInstructionUseCase struct {
	text          instructionTextSender
	template      instructionTemplateSender
	conversations instructionConversationFinder
	recorder      instructionConversationRecorder
	accounts      instructionAccountFinder
	drivers       DriverLookup
	charger       UsageCharger
	reporter      DriverInstructionReporter
	claims        EventClaimStore
	clock         Clock
	templateName  string
	language      string
	log           *slog.Logger
}

type DriverJobInstructionDeps struct {
	Text          instructionTextSender
	Template      instructionTemplateSender
	Conversations instructionConversationFinder
	Recorder      instructionConversationRecorder
	Accounts      instructionAccountFinder
	Drivers       DriverLookup
	Charger       UsageCharger
	Reporter      DriverInstructionReporter
	Claims        EventClaimStore
	Clock         Clock
	TemplateName  string
	Language      string
	Log           *slog.Logger
}

func NewConsumeDriverJobInstructionUseCase(deps DriverJobInstructionDeps) *ConsumeDriverJobInstructionUseCase {
	return &ConsumeDriverJobInstructionUseCase{
		text:          deps.Text,
		template:      deps.Template,
		conversations: deps.Conversations,
		recorder:      deps.Recorder,
		accounts:      deps.Accounts,
		drivers:       deps.Drivers,
		charger:       deps.Charger,
		reporter:      deps.Reporter,
		claims:        deps.Claims,
		clock:         deps.Clock,
		templateName:  strings.TrimSpace(deps.TemplateName),
		language:      strings.TrimSpace(deps.Language),
		log:           deps.Log,
	}
}

type driverInstruction struct {
	tenantID, eventID                  string
	jobID, driverID, phone             string
	reference, pickup, delivery, cargo string
	priority, message                  string
}

func parseDriverInstruction(envelope eventbus.EventEnvelope) driverInstruction {
	return driverInstruction{
		tenantID:  envelope.TenantID,
		eventID:   envelope.ID,
		jobID:     strings.TrimSpace(dataText(envelope.Data, "job_id")),
		driverID:  strings.TrimSpace(dataText(envelope.Data, "driver_id")),
		phone:     strings.TrimSpace(dataText(envelope.Data, "driver_phone")),
		reference: strings.TrimSpace(dataText(envelope.Data, "job_reference")),
		pickup:    strings.TrimSpace(dataText(envelope.Data, "pickup_location")),
		delivery:  strings.TrimSpace(dataText(envelope.Data, "delivery_location")),
		cargo:     strings.TrimSpace(dataText(envelope.Data, "cargo_description")),
		priority:  strings.TrimSpace(dataText(envelope.Data, "priority")),
		message:   strings.TrimSpace(dataText(envelope.Data, "message")),
	}
}

// Handle messages the driver a job was just assigned to. The event must have
// passed bus signature verification before it gets here. A send that cannot
// succeed is reported to dispatch and acknowledged; only an infrastructure
// error that a redelivery could fix is returned.
func (u *ConsumeDriverJobInstructionUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	if envelope.Type != DriverJobInstructionEventType || envelope.ID == "" || strings.TrimSpace(envelope.TenantID) == "" {
		u.log.Warn("driver_instruction_invalid", "subject", envelope.Subject, "event", envelope.ID)
		return nil
	}
	instruction := parseDriverInstruction(envelope)
	if instruction.jobID == "" || instruction.driverID == "" {
		u.log.Warn("driver_instruction_invalid", "tenant", envelope.TenantID, "event", envelope.ID)
		return nil
	}

	claimed, err := u.claims.Claim(ctx, "driver_instruction|"+envelope.ID, eventClaimTTL)
	if err != nil {
		return fmt.Errorf("claim driver instruction %s: %w", envelope.ID, err)
	}
	if !claimed {
		return nil
	}

	report, err := u.deliver(ctx, instruction)
	if err != nil {
		_ = u.claims.Release(ctx, "driver_instruction|"+envelope.ID)
		return err
	}
	u.report(ctx, instruction, report)
	return nil
}

type instructionResult struct{ outcome, reason string }

func (u *ConsumeDriverJobInstructionUseCase) deliver(ctx context.Context, instruction driverInstruction) (instructionResult, error) {
	if instruction.phone == "" {
		return instructionResult{InstructionOutcomeNotSent, "the driver has no phone number on this job"}, nil
	}
	hasNumber, err := u.hasWhatsAppNumber(ctx, instruction.tenantID)
	if err != nil {
		return instructionResult{}, err
	}
	if !hasNumber {
		return instructionResult{InstructionOutcomeNotSent, "no WhatsApp number"}, nil
	}

	open, err := u.windowIsOpen(ctx, instruction)
	if err != nil {
		return instructionResult{}, err
	}
	var sendErr error
	if open {
		sendErr = u.sendText(ctx, instruction)
	} else {
		sendErr = u.sendTemplate(ctx, instruction)
	}
	if sendErr != nil {
		return instructionResult{InstructionOutcomeFailed, failureReason(sendErr)}, nil
	}
	return instructionResult{InstructionOutcomeSent, ""}, nil
}

func (u *ConsumeDriverJobInstructionUseCase) hasWhatsAppNumber(ctx context.Context, tenantID string) (bool, error) {
	_, found, err := u.accounts.FindAccountByTenantID(ctx, tenantID)
	if err != nil {
		return false, fmt.Errorf("read whatsapp account for %s: %w", tenantID, err)
	}
	return found, nil
}

// windowIsOpen is whether the driver wrote to the tenant's number in the last
// 24 hours. Inside it a plain message is allowed; outside, only a template.
func (u *ConsumeDriverJobInstructionUseCase) windowIsOpen(ctx context.Context, instruction driverInstruction) (bool, error) {
	conversation, found, err := u.conversations.FindConversationByTenantAndDriverPhone(ctx, instruction.tenantID, normalizeConversationPhone(instruction.phone))
	if err != nil {
		return false, fmt.Errorf("read conversation for driver %s: %w", instruction.driverID, err)
	}
	return found && !conversation.IsLocked(u.clock.Now()), nil
}

func (u *ConsumeDriverJobInstructionUseCase) sendText(ctx context.Context, instruction driverInstruction) error {
	if instruction.message == "" {
		return errors.New("the instruction has no message")
	}
	message, err := u.text.Execute(ctx, SendMessageCommand{
		TenantID:         instruction.tenantID,
		Channel:          "whatsapp",
		Recipient:        instruction.phone,
		Content:          instruction.message,
		NotificationType: domain.NotificationTypeJobAssignment,
		MessageID:        stableMessageID(instruction.eventID),
	})
	if err != nil {
		return err
	}
	u.recordText(ctx, instruction, message)
	return nil
}

// recordText keeps the instruction in the conversation as a job_instruction so
// a reply or a failed receipt resolves to this job. The message already left,
// so a storage failure is logged rather than failing the send.
func (u *ConsumeDriverJobInstructionUseCase) recordText(ctx context.Context, instruction driverInstruction, message domain.OutboxMessage) {
	err := u.recorder.RecordJobInstructionText(ctx, JobInstructionTextRecord{
		TenantID:          instruction.tenantID,
		JobID:             instruction.jobID,
		DriverID:          instruction.driverID,
		DriverName:        u.driverName(ctx, instruction),
		Recipient:         instruction.phone,
		Text:              instruction.message,
		ProviderMessageID: message.ProviderMessageID,
		Status:            message.Status,
		JobReference:      instruction.reference,
		PickupLocation:    instruction.pickup,
		DeliveryLocation:  instruction.delivery,
		CargoDescription:  instruction.cargo,
		Priority:          instruction.priority,
	})
	if err != nil {
		u.log.Error("driver_instruction_record_failed", "tenant", instruction.tenantID, "job", instruction.jobID, "error", err)
	}
}

func (u *ConsumeDriverJobInstructionUseCase) sendTemplate(ctx context.Context, instruction driverInstruction) error {
	if u.templateName == "" {
		return errors.New("no job instruction template is configured")
	}
	_, err := u.template.Execute(ctx, SendJobInstructionCommand{
		TenantID:         instruction.tenantID,
		JobID:            instruction.jobID,
		DriverID:         instruction.driverID,
		Recipient:        instruction.phone,
		TemplateName:     u.templateName,
		Language:         u.language,
		DriverName:       u.driverName(ctx, instruction),
		JobReference:     instruction.reference,
		PickupLocation:   instruction.pickup,
		DeliveryLocation: instruction.delivery,
		CargoDescription: instruction.cargo,
		Priority:         instruction.priority,
		BeforeSend:       func(ctx context.Context) error { return u.chargeTemplate(ctx, instruction) },
	})
	return err
}

// chargeTemplate debits the wallet before the template goes out, the same rule
// every other tenant send follows. The text path is charged by SendMessage.
func (u *ConsumeDriverJobInstructionUseCase) chargeTemplate(ctx context.Context, instruction driverInstruction) error {
	if u.charger == nil || instruction.tenantID == identity.SystemTenantID {
		return nil
	}
	return u.charger.Charge(ctx, UsageCharge{
		TenantID:  instruction.tenantID,
		Channel:   "whatsapp",
		Quantity:  1,
		Reason:    "whatsapp job instruction to " + instruction.phone,
		Reference: stableMessageID(instruction.eventID),
	})
}

func (u *ConsumeDriverJobInstructionUseCase) driverName(ctx context.Context, instruction driverInstruction) string {
	if u.drivers == nil {
		return defaultInstructionDriverName
	}
	linked, err := u.drivers.DriversByPhone(ctx, instruction.phone)
	if err != nil {
		return defaultInstructionDriverName
	}
	for _, driver := range linked {
		if driver.DriverID == instruction.driverID && driver.TenantID == instruction.tenantID && strings.TrimSpace(driver.Name) != "" {
			return strings.TrimSpace(driver.Name)
		}
	}
	return defaultInstructionDriverName
}

func (u *ConsumeDriverJobInstructionUseCase) report(ctx context.Context, instruction driverInstruction, result instructionResult) {
	err := u.reporter.ReportDriverInstruction(ctx, DriverInstructionReport{
		TenantID: instruction.tenantID,
		JobID:    instruction.jobID,
		DriverID: instruction.driverID,
		Outcome:  result.outcome,
		Channel:  "whatsapp",
		Reason:   result.reason,
	})
	if err != nil {
		u.log.Error("driver_instruction_report_failed", "tenant", instruction.tenantID, "job", instruction.jobID, "outcome", result.outcome, "error", err)
	}
}

func failureReason(err error) string {
	switch {
	case errors.Is(err, ErrInsufficientFunds):
		return "wallet balance is too low"
	case errors.Is(err, ErrChannelDisabled):
		return "WhatsApp messaging is switched off"
	default:
		return err.Error()
	}
}
