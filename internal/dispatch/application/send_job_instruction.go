package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type JobInstructionHeaderMedia struct {
	Type     string
	Link     string
	ID       string
	Filename string
}

type JobInstructionButtonParameter struct {
	Index   int
	Type    string
	Text    string
	Payload string
}

type DispatchJobInstructionPayload struct {
	TenantID         string
	JobID            string
	DriverID         string
	Recipient        string
	TemplateName     string
	Language         string
	DriverName       string
	JobReference     string
	PickupLocation   string
	DeliveryLocation string
	CargoDescription string
	Priority         string
	HeaderMedia      *JobInstructionHeaderMedia
	ButtonParameters []JobInstructionButtonParameter
}

type JobInstructionSender interface {
	SendJobInstruction(ctx context.Context, payload DispatchJobInstructionPayload) error
}

type SendJobInstructionCommand struct {
	TenantID         string
	JobID            string
	DriverID         string
	Recipient        string
	TemplateName     string
	Language         string
	DriverName       string
	HeaderMedia      *JobInstructionHeaderMedia
	ButtonParameters []JobInstructionButtonParameter
}

type SendJobInstructionUseCase struct {
	repository    JobRepository
	sender        JobInstructionSender
	clock         Clock
	idGenerator   IDGenerator
	defaultTenant string
}

var (
	ErrJobInstructionJobIDRequired         = errors.New("job id is required")
	ErrJobInstructionDriverIDRequired      = errors.New("driver id is required")
	ErrJobInstructionAssignedPhoneRequired = errors.New("assigned driver phone is not stored on this job")
	ErrJobInstructionRecipientMismatch     = errors.New("job instruction can only be sent to the assigned driver's stored phone number")
	ErrJobInstructionTemplateNameRequired  = errors.New("template name is required")
	ErrJobInstructionDriverNameRequired    = errors.New("driver name is required")
	ErrJobInstructionCargoRequired         = errors.New("job must include cargo details before sending driver instruction")
	ErrJobInstructionJobNotAssigned        = errors.New("job must be assigned before sending driver instruction")
	ErrJobInstructionDriverMismatch        = errors.New("job instruction can only be sent to the driver assigned to this job")
	ErrJobInstructionStatusNotSendable     = errors.New("job instruction can only be sent while the job is assigned")
)

func NewSendJobInstructionUseCase(
	repository JobRepository,
	sender JobInstructionSender,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
) *SendJobInstructionUseCase {
	return &SendJobInstructionUseCase{
		repository:    repository,
		sender:        sender,
		clock:         clock,
		idGenerator:   idGenerator,
		defaultTenant: defaultTenant,
	}
}

func (u *SendJobInstructionUseCase) Execute(ctx context.Context, command SendJobInstructionCommand) (domain.Job, error) {
	if strings.TrimSpace(command.JobID) == "" {
		return domain.Job{}, ErrJobInstructionJobIDRequired
	}
	if strings.TrimSpace(command.DriverID) == "" {
		return domain.Job{}, ErrJobInstructionDriverIDRequired
	}
	if strings.TrimSpace(command.TemplateName) == "" {
		return domain.Job{}, ErrJobInstructionTemplateNameRequired
	}
	if strings.TrimSpace(command.DriverName) == "" {
		return domain.Job{}, ErrJobInstructionDriverNameRequired
	}

	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}
	if strings.TrimSpace(job.AssignedDriverID) == "" {
		return domain.Job{}, ErrJobInstructionJobNotAssigned
	}
	if strings.TrimSpace(command.DriverID) != strings.TrimSpace(job.AssignedDriverID) {
		return domain.Job{}, ErrJobInstructionDriverMismatch
	}
	if job.Status != domain.StatusAssigned {
		return domain.Job{}, ErrJobInstructionStatusNotSendable
	}
	storedRecipient := strings.TrimSpace(job.AssignedDriverPhone)
	if storedRecipient == "" {
		return domain.Job{}, ErrJobInstructionAssignedPhoneRequired
	}
	if requestedRecipient := strings.TrimSpace(command.Recipient); requestedRecipient != "" && normalizeDispatchPhone(requestedRecipient) != normalizeDispatchPhone(storedRecipient) {
		return domain.Job{}, ErrJobInstructionRecipientMismatch
	}

	cargoDescription := buildDispatchCargoDescription(job)
	if cargoDescription == "" {
		return domain.Job{}, ErrJobInstructionCargoRequired
	}

	payload := DispatchJobInstructionPayload{
		TenantID:         job.TenantID,
		JobID:            job.ID,
		DriverID:         job.AssignedDriverID,
		Recipient:        storedRecipient,
		TemplateName:     strings.TrimSpace(command.TemplateName),
		Language:         strings.TrimSpace(command.Language),
		DriverName:       strings.TrimSpace(command.DriverName),
		JobReference:     firstNonEmptyDispatchValue(job.Reference, job.ID),
		PickupLocation:   strings.TrimSpace(job.Pickup.Address),
		DeliveryLocation: strings.TrimSpace(job.Dropoff.Address),
		CargoDescription: cargoDescription,
		Priority:         string(job.Priority),
		HeaderMedia:      command.HeaderMedia,
		ButtonParameters: append([]JobInstructionButtonParameter{}, command.ButtonParameters...),
	}

	if err := u.sender.SendJobInstruction(ctx, payload); err != nil {
		return domain.Job{}, err
	}

	updatedJob := job.MarkDriverInstructionSent("whatsapp", u.clock.Now(), u.idGenerator.New())
	if err := u.repository.Save(updatedJob); err != nil {
		// The WhatsApp send has already succeeded at this point; returning the persistence error
		// keeps the failure visible instead of falsely claiming the job timeline was updated.
		return domain.Job{}, fmt.Errorf("save job after driver instruction send: %w", err)
	}

	return updatedJob, nil
}

func (u *SendJobInstructionUseCase) findJobForTenant(tenantID, jobID string) (domain.Job, error) {
	resolvedTenant := u.resolveTenant(tenantID)
	job, ok := u.repository.FindByTenantAndID(resolvedTenant, strings.TrimSpace(jobID))
	if !ok {
		return domain.Job{}, ErrJobNotFound
	}

	return job, nil
}

func (u *SendJobInstructionUseCase) resolveTenant(tenantID string) string {
	resolved := strings.TrimSpace(tenantID)
	if resolved == "" {
		return u.defaultTenant
	}
	return resolved
}

func buildDispatchCargoDescription(job domain.Job) string {
	if description := strings.TrimSpace(job.CargoDescription); description != "" {
		return description
	}
	cargoType := strings.TrimSpace(job.CargoType)
	if cargoType == "" && job.LoadWeightKg <= 0 {
		return ""
	}
	if cargoType == "" {
		return fmt.Sprintf("%.0fkg load", job.LoadWeightKg)
	}
	if job.LoadWeightKg <= 0 {
		return cargoType
	}
	return fmt.Sprintf("%s, %.0fkg", cargoType, job.LoadWeightKg)
}

func firstNonEmptyDispatchValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeDispatchPhone(value string) string {
	normalized := strings.TrimSpace(value)
	normalized = strings.TrimPrefix(normalized, "whatsapp:")
	replacer := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	normalized = replacer.Replace(normalized)
	return strings.TrimPrefix(normalized, "+")
}
