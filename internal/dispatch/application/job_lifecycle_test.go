package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type lifecycleRepository struct {
	data map[string]domain.Job
}

func newLifecycleRepository() *lifecycleRepository {
	return &lifecycleRepository{
		data: make(map[string]domain.Job),
	}
}

func (r *lifecycleRepository) Save(job domain.Job) error {
	r.data[fmt.Sprintf("%s:%s", job.TenantID, job.ID)] = job
	return nil
}

func (r *lifecycleRepository) FindByTenantAndID(tenantID, id string) (domain.Job, bool) {
	job, ok := r.data[fmt.Sprintf("%s:%s", tenantID, id)]
	return job, ok
}

func (r *lifecycleRepository) FindByTenant(tenantID string) []domain.Job {
	results := make([]domain.Job, 0)
	for _, job := range r.data {
		if job.TenantID == tenantID {
			results = append(results, job)
		}
	}
	return results
}

type lifecycleEmitter struct {
	commands []PublishJobStatusChangedCommand
}

func (e *lifecycleEmitter) Execute(
	_ context.Context,
	command PublishJobStatusChangedCommand,
) (PublishJobStatusChangedResult, error) {
	e.commands = append(e.commands, command)
	return PublishJobStatusChangedResult{Published: true}, nil
}

type lifecycleNotifier struct {
	calls int
	err   error
}

func (n *lifecycleNotifier) NotifyJobInstruction(
	_ context.Context,
	_, _, _, _ string,
) error {
	n.calls++
	return n.err
}

type lifecycleClock struct {
	now time.Time
}

func (c lifecycleClock) Now() time.Time {
	return c.now
}

type lifecycleID struct {
	next int
}

func (i *lifecycleID) New() string {
	i.next++
	return fmt.Sprintf("id-%d", i.next)
}

func TestJobLifecycleUseCase_HappyPath(t *testing.T) {
	repo := newLifecycleRepository()
	emitter := &lifecycleEmitter{}
	notifier := &lifecycleNotifier{}
	ids := &lifecycleID{}
	useCase := NewJobLifecycleUseCase(
		repo,
		emitter,
		notifier,
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		ids,
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID: "job-1",
		Pickup: domain.JobStop{
			Address: "Apapa Port",
		},
		Dropoff: domain.JobStop{
			Address: "Lekki Free Zone",
		},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	job, err = useCase.AssignJob(context.Background(), AssignJobCommand{
		JobID:     job.ID,
		DriverID:  "driver-1",
		VehicleID: "vehicle-1",
	})
	if err != nil {
		t.Fatalf("assign job failed: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("expected notifier to be called once, got %d", notifier.calls)
	}

	job, err = useCase.AcknowledgeJob(context.Background(), AcknowledgeJobCommand{
		JobID:    job.ID,
		DriverID: "driver-1",
	})
	if err != nil {
		t.Fatalf("acknowledge job failed: %v", err)
	}
	if job.AcknowledgedAt == nil {
		t.Fatalf("expected acknowledgement timestamp")
	}

	for _, status := range []string{"en-route", "arrived", "loaded", "delivered"} {
		job, err = useCase.UpdateJobStatus(context.Background(), UpdateJobStatusCommand{
			JobID:  job.ID,
			Status: status,
		})
		if err != nil {
			t.Fatalf("update status %s failed: %v", status, err)
		}
	}

	job, err = useCase.AddPODAttachment(context.Background(), AddJobPODAttachmentCommand{
		JobID: job.ID,
		Kind:  "photo",
		URL:   "https://example.com/pod.jpg",
	})
	if err != nil {
		t.Fatalf("add pod attachment failed: %v", err)
	}
	if len(job.PODAttachments) != 1 {
		t.Fatalf("expected one pod attachment, got %d", len(job.PODAttachments))
	}

	job, err = useCase.AddNote(context.Background(), AddJobNoteCommand{
		JobID: job.ID,
		Text:  "Customer received goods",
	})
	if err != nil {
		t.Fatalf("add note failed: %v", err)
	}
	if len(job.Notes) != 1 {
		t.Fatalf("expected one note, got %d", len(job.Notes))
	}

	timeline, err := useCase.GetTimeline(context.Background(), "", job.ID)
	if err != nil {
		t.Fatalf("get timeline failed: %v", err)
	}
	if len(timeline) == 0 {
		t.Fatalf("expected timeline entries")
	}
	if len(emitter.commands) < 5 {
		t.Fatalf("expected at least 5 emitted status events, got %d", len(emitter.commands))
	}
}

func TestJobLifecycleUseCase_CancelRequiresReason(t *testing.T) {
	useCase := NewJobLifecycleUseCase(
		newLifecycleRepository(),
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID: "job-2",
		Pickup: domain.JobStop{
			Address: "Ikeja",
		},
		Dropoff: domain.JobStop{
			Address: "Yaba",
		},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	_, err = useCase.CancelJob(context.Background(), CancelJobCommand{
		JobID:  job.ID,
		Reason: " ",
	})
	if err == nil {
		t.Fatalf("expected cancellation error for empty reason")
	}
}

func TestJobLifecycleUseCase_TenantIsolation(t *testing.T) {
	useCase := NewJobLifecycleUseCase(
		newLifecycleRepository(),
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		TenantID: "tenant-a",
		JobID:    "job-3",
		Pickup: domain.JobStop{
			Address: "Ojota",
		},
		Dropoff: domain.JobStop{
			Address: "Surulere",
		},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	_, err = useCase.GetJob(context.Background(), "tenant-b", job.ID)
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}
