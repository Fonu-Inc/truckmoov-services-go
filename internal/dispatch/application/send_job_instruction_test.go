package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type jobInstructionSenderStub struct {
	payloads []DispatchJobInstructionPayload
	err      error
}

func (s *jobInstructionSenderStub) SendJobInstruction(_ context.Context, payload DispatchJobInstructionPayload) error {
	s.payloads = append(s.payloads, payload)
	return s.err
}

func TestSendJobInstructionUseCaseExecute(t *testing.T) {
	repo := newLifecycleRepository()
	sender := &jobInstructionSenderStub{}
	ids := &lifecycleID{}
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID:                  "job-1",
		Reference:           "TM-2847",
		TenantID:            "tenant-1",
		Pickup:              domain.JobStop{Address: "Apapa Port"},
		Dropoff:             domain.JobStop{Address: "Ikeja Industrial Estate"},
		CargoDescription:    "Electronics, 50kg",
		Priority:            domain.PriorityUrgent,
		Status:              domain.StatusAssigned,
		AssignedDriverID:    "driver-1",
		AssignedDriverPhone: "+2348012345678",
		AssignedVehicleID:   "vehicle-1",
		CreatedAt:           now,
		UpdatedAt:           now,
		Timeline:            []domain.JobTimelineEntry{},
		Notes:               []domain.JobNote{},
		PODAttachments:      []domain.JobPODAttachment{},
	})
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := repo.Save(job); err != nil {
		t.Fatalf("save seed job: %v", err)
	}

	useCase := NewSendJobInstructionUseCase(
		repo,
		sender,
		lifecycleClock{now: now},
		ids,
		"default",
	)

	updated, err := useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:     "tenant-1",
		JobID:        "job-1",
		DriverID:     "driver-1",
		TemplateName: "job_assignment_v1",
		Language:     "en_US",
		DriverName:   "Chukwuma Okafor",
	})
	if err != nil {
		t.Fatalf("execute send job instruction: %v", err)
	}
	if len(sender.payloads) != 1 {
		t.Fatalf("expected one send payload, got %d", len(sender.payloads))
	}
	if sender.payloads[0].JobReference != "TM-2847" {
		t.Fatalf("expected job reference TM-2847, got %q", sender.payloads[0].JobReference)
	}
	if sender.payloads[0].CargoDescription != "Electronics, 50kg" {
		t.Fatalf("expected cargo description, got %q", sender.payloads[0].CargoDescription)
	}
	if sender.payloads[0].Recipient != "+2348012345678" {
		t.Fatalf("expected stored assigned driver phone to be used, got %q", sender.payloads[0].Recipient)
	}
	if sender.payloads[0].Priority != "urgent" {
		t.Fatalf("expected urgent priority, got %q", sender.payloads[0].Priority)
	}
	found := false
	for _, entry := range updated.Timeline {
		if entry.Type == "driver_instruction_sent" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected driver_instruction_sent timeline entry")
	}
}

func TestSendJobInstructionUseCaseExecuteRequiresCargo(t *testing.T) {
	repo := newLifecycleRepository()
	sender := &jobInstructionSenderStub{}
	ids := &lifecycleID{}
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID:                  "job-1",
		TenantID:            "tenant-1",
		Pickup:              domain.JobStop{Address: "Apapa Port"},
		Dropoff:             domain.JobStop{Address: "Ikeja Industrial Estate"},
		Priority:            domain.PriorityNormal,
		Status:              domain.StatusAssigned,
		AssignedDriverID:    "driver-1",
		AssignedDriverPhone: "+2348012345678",
		AssignedVehicleID:   "vehicle-1",
		CreatedAt:           now,
		UpdatedAt:           now,
		Timeline:            []domain.JobTimelineEntry{},
		Notes:               []domain.JobNote{},
		PODAttachments:      []domain.JobPODAttachment{},
	})
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := repo.Save(job); err != nil {
		t.Fatalf("save seed job: %v", err)
	}

	useCase := NewSendJobInstructionUseCase(repo, sender, lifecycleClock{now: now}, ids, "default")

	_, err = useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:     "tenant-1",
		JobID:        "job-1",
		DriverID:     "driver-1",
		TemplateName: "job_assignment_v1",
		DriverName:   "Chukwuma Okafor",
	})
	if !errors.Is(err, ErrJobInstructionCargoRequired) {
		t.Fatalf("expected ErrJobInstructionCargoRequired, got %v", err)
	}
}

func TestSendJobInstructionUseCaseExecuteRejectsNonAssignedDriver(t *testing.T) {
	repo := newLifecycleRepository()
	sender := &jobInstructionSenderStub{}
	ids := &lifecycleID{}
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID:                  "job-1",
		Reference:           "TM-2847",
		TenantID:            "tenant-1",
		Pickup:              domain.JobStop{Address: "Apapa Port"},
		Dropoff:             domain.JobStop{Address: "Ikeja Industrial Estate"},
		CargoDescription:    "Electronics, 50kg",
		Priority:            domain.PriorityUrgent,
		Status:              domain.StatusAssigned,
		AssignedDriverID:    "driver-1",
		AssignedDriverPhone: "+2348012345678",
		AssignedVehicleID:   "vehicle-1",
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := repo.Save(job); err != nil {
		t.Fatalf("save seed job: %v", err)
	}

	useCase := NewSendJobInstructionUseCase(repo, sender, lifecycleClock{now: now}, ids, "default")

	_, err = useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:     "tenant-1",
		JobID:        "job-1",
		DriverID:     "driver-2",
		TemplateName: "job_assignment_v1",
		DriverName:   "Chukwuma Okafor",
	})
	if !errors.Is(err, ErrJobInstructionDriverMismatch) {
		t.Fatalf("expected ErrJobInstructionDriverMismatch, got %v", err)
	}
}

func TestSendJobInstructionUseCaseExecuteRejectsJobPastAssigned(t *testing.T) {
	repo := newLifecycleRepository()
	sender := &jobInstructionSenderStub{}
	ids := &lifecycleID{}
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID:                  "job-1",
		Reference:           "TM-2847",
		TenantID:            "tenant-1",
		Pickup:              domain.JobStop{Address: "Apapa Port"},
		Dropoff:             domain.JobStop{Address: "Ikeja Industrial Estate"},
		CargoDescription:    "Electronics, 50kg",
		Priority:            domain.PriorityUrgent,
		Status:              domain.StatusEnRoute,
		AssignedDriverID:    "driver-1",
		AssignedDriverPhone: "+2348012345678",
		AssignedVehicleID:   "vehicle-1",
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := repo.Save(job); err != nil {
		t.Fatalf("save seed job: %v", err)
	}

	useCase := NewSendJobInstructionUseCase(repo, sender, lifecycleClock{now: now}, ids, "default")

	_, err = useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:     "tenant-1",
		JobID:        "job-1",
		DriverID:     "driver-1",
		TemplateName: "job_assignment_v1",
		DriverName:   "Chukwuma Okafor",
	})
	if !errors.Is(err, ErrJobInstructionStatusNotSendable) {
		t.Fatalf("expected ErrJobInstructionStatusNotSendable, got %v", err)
	}
}

func TestSendJobInstructionUseCaseExecuteRequiresStoredAssignedDriverPhone(t *testing.T) {
	repo := newLifecycleRepository()
	sender := &jobInstructionSenderStub{}
	ids := &lifecycleID{}
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID:                "job-1",
		Reference:         "TM-2847",
		TenantID:          "tenant-1",
		Pickup:            domain.JobStop{Address: "Apapa Port"},
		Dropoff:           domain.JobStop{Address: "Ikeja Industrial Estate"},
		CargoDescription:  "Electronics, 50kg",
		Priority:          domain.PriorityUrgent,
		Status:            domain.StatusAssigned,
		AssignedDriverID:  "driver-1",
		AssignedVehicleID: "vehicle-1",
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := repo.Save(job); err != nil {
		t.Fatalf("save seed job: %v", err)
	}

	useCase := NewSendJobInstructionUseCase(repo, sender, lifecycleClock{now: now}, ids, "default")

	_, err = useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:     "tenant-1",
		JobID:        "job-1",
		DriverID:     "driver-1",
		TemplateName: "job_assignment_v1",
		DriverName:   "Chukwuma Okafor",
	})
	if !errors.Is(err, ErrJobInstructionAssignedPhoneRequired) {
		t.Fatalf("expected ErrJobInstructionAssignedPhoneRequired, got %v", err)
	}
}

func TestSendJobInstructionUseCaseExecuteRejectsMismatchedRecipientPhone(t *testing.T) {
	repo := newLifecycleRepository()
	sender := &jobInstructionSenderStub{}
	ids := &lifecycleID{}
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID:                  "job-1",
		Reference:           "TM-2847",
		TenantID:            "tenant-1",
		Pickup:              domain.JobStop{Address: "Apapa Port"},
		Dropoff:             domain.JobStop{Address: "Ikeja Industrial Estate"},
		CargoDescription:    "Electronics, 50kg",
		Priority:            domain.PriorityUrgent,
		Status:              domain.StatusAssigned,
		AssignedDriverID:    "driver-1",
		AssignedDriverPhone: "+2348012345678",
		AssignedVehicleID:   "vehicle-1",
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := repo.Save(job); err != nil {
		t.Fatalf("save seed job: %v", err)
	}

	useCase := NewSendJobInstructionUseCase(repo, sender, lifecycleClock{now: now}, ids, "default")

	_, err = useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:     "tenant-1",
		JobID:        "job-1",
		DriverID:     "driver-1",
		Recipient:    "+2348099999999",
		TemplateName: "job_assignment_v1",
		DriverName:   "Chukwuma Okafor",
	})
	if !errors.Is(err, ErrJobInstructionRecipientMismatch) {
		t.Fatalf("expected ErrJobInstructionRecipientMismatch, got %v", err)
	}
}
