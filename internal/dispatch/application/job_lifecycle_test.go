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
	data        map[string]domain.Job
	deleteCalls int
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

func (r *lifecycleRepository) Delete(tenantID, id string) error {
	r.deleteCalls++
	delete(r.data, fmt.Sprintf("%s:%s", tenantID, id))
	return nil
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

func (r *lifecycleRepository) FindByID(id string) (domain.Job, bool) {
	for _, job := range r.data {
		if job.ID == id {
			return job, true
		}
	}
	return domain.Job{}, false
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
		JobID:       job.ID,
		DriverID:    "driver-1",
		DriverPhone: "+2348010000001",
		VehicleID:   "vehicle-1",
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

func TestJobLifecycleUseCase_CreateJobMapsUIDetails(t *testing.T) {
	repo := newLifecycleRepository()
	emitter := &lifecycleEmitter{}
	notifier := &lifecycleNotifier{}
	useCase := NewJobLifecycleUseCase(
		repo,
		emitter,
		notifier,
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	deadline := time.Date(2026, 2, 11, 16, 30, 0, 0, time.UTC)
	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		Reference:        "JOB-900",
		CargoType:        "Electronics",
		CargoDescription: "Fragile televisions",
		InitialNote:      "Handle with care",
		AmountNgn:        900000,
		Deadline:         &deadline,
		LoadWeightKg:     6500,
		Priority:         "urgent",
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

	if job.Reference != "JOB-900" {
		t.Fatalf("expected reference JOB-900, got %q", job.Reference)
	}
	if job.CargoType != "Electronics" {
		t.Fatalf("expected cargo type Electronics, got %q", job.CargoType)
	}
	if job.AmountNgn != 900000 {
		t.Fatalf("expected amount 900000, got %v", job.AmountNgn)
	}
	if job.Priority != domain.PriorityUrgent {
		t.Fatalf("expected urgent to normalize to %q, got %q", domain.PriorityUrgent, job.Priority)
	}
	if job.Deadline == nil || !job.Deadline.Equal(deadline) {
		t.Fatalf("expected deadline to be stored")
	}
	if job.TimeWindow.DropoffEnd == nil || !job.TimeWindow.DropoffEnd.Equal(deadline) {
		t.Fatalf("expected deadline to populate dropoff end window")
	}
	if job.Status != domain.StatusCreated {
		t.Fatalf("expected job creation to never assign, got %q", job.Status)
	}
	if job.AssignedVehicleID != "" || job.AssignedDriverID != "" || job.AssignedDriverPhone != "" {
		t.Fatalf("expected no vehicle/driver assigned at creation, got vehicle=%q driver=%q phone=%q", job.AssignedVehicleID, job.AssignedDriverID, job.AssignedDriverPhone)
	}
	if len(job.Notes) != 1 || job.Notes[0].Text != "Handle with care" {
		t.Fatalf("expected initial note to be stored, got %#v", job.Notes)
	}
	if len(emitter.commands) != 0 {
		t.Fatalf("expected no emitted status event at creation, got %d", len(emitter.commands))
	}
	if notifier.calls != 0 {
		t.Fatalf("expected no driver notification at creation, got %d", notifier.calls)
	}
}

func TestJobLifecycleUseCase_ListJobsSupportsFilters(t *testing.T) {
	repo := newLifecycleRepository()
	useCase := NewJobLifecycleUseCase(
		repo,
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	jobA, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:     "job-a",
		Reference: "TM-2849",
		Pickup:    domain.JobStop{Address: "Oshodi Market"},
		Dropoff:   domain.JobStop{Address: "Ikorodu"},
	})
	if err != nil {
		t.Fatalf("create job A failed: %v", err)
	}

	jobB, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:     "job-b",
		Reference: "TM-2850",
		Pickup:    domain.JobStop{Address: "Apapa Port"},
		Dropoff:   domain.JobStop{Address: "Victoria Island"},
	})
	if err != nil {
		t.Fatalf("create job B failed: %v", err)
	}
	if _, err := useCase.AssignJob(context.Background(), AssignJobCommand{
		JobID:     jobB.ID,
		VehicleID: "vehicle-2",
		DriverID:  "driver-2",
	}); err != nil {
		t.Fatalf("assign job B failed: %v", err)
	}

	list, err := useCase.ListJobs(context.Background(), "", ListJobsFilter{
		Status: "pending",
		Query:  "oshodi",
	})
	if err != nil {
		t.Fatalf("list jobs failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one pending oshodi job, got %d", len(list))
	}
	if list[0].ID != jobA.ID {
		t.Fatalf("expected job %q, got %q", jobA.ID, list[0].ID)
	}

	list, err = useCase.ListJobs(context.Background(), "", ListJobsFilter{
		Status:    "assigned",
		VehicleID: "vehicle-2",
		DriverID:  "driver-2",
	})
	if err != nil {
		t.Fatalf("list jobs with assigned failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != "job-b" {
		t.Fatalf("expected assigned job-b, got %#v", list)
	}

	// Matches the summary, which counts an assigned job as assigned.
	list, err = useCase.ListJobs(context.Background(), "", ListJobsFilter{Status: "in_progress"})
	if err != nil {
		t.Fatalf("list jobs with in_progress failed: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected in_progress to exclude assigned jobs, got %#v", list)
	}
}

func TestJobLifecycleUseCase_ListJobsSupportsWeightAmountDeadlineFilters(t *testing.T) {
	repo := newLifecycleRepository()
	useCase := NewJobLifecycleUseCase(
		repo,
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	deadline := time.Date(2026, 3, 15, 18, 0, 0, 0, time.UTC)
	if _, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID: "light", Pickup: domain.JobStop{Address: "A"}, Dropoff: domain.JobStop{Address: "B"},
		LoadWeightKg: 2000, AmountNgn: 50000, Deadline: &deadline,
	}); err != nil {
		t.Fatalf("create light: %v", err)
	}
	if _, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID: "heavy", Pickup: domain.JobStop{Address: "A"}, Dropoff: domain.JobStop{Address: "B"},
		LoadWeightKg: 9000, AmountNgn: 300000,
	}); err != nil {
		t.Fatalf("create heavy: %v", err)
	}

	f := func(v float64) *float64 { return &v }

	// Weight 3-8 tons (3000-8000 kg) matches neither (light=2t, heavy=9t).
	if got, _ := useCase.ListJobs(context.Background(), "", ListJobsFilter{WeightFromKg: f(3000), WeightToKg: f(8000)}); len(got) != 0 {
		t.Fatalf("weight 3-8t expected 0, got %d", len(got))
	}
	// Weight >= 5t matches only heavy.
	got, _ := useCase.ListJobs(context.Background(), "", ListJobsFilter{WeightFromKg: f(5000)})
	if len(got) != 1 || got[0].ID != "heavy" {
		t.Fatalf("weight >=5t expected [heavy], got %#v", got)
	}
	// Amount <= 100000 matches only light.
	got, _ = useCase.ListJobs(context.Background(), "", ListJobsFilter{AmountTo: f(100000)})
	if len(got) != 1 || got[0].ID != "light" {
		t.Fatalf("amount <=100k expected [light], got %#v", got)
	}
	// Deadline on 2026-03-15 matches only light (heavy has no deadline).
	d := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	got, _ = useCase.ListJobs(context.Background(), "", ListJobsFilter{Deadline: &d})
	if len(got) != 1 || got[0].ID != "light" {
		t.Fatalf("deadline filter expected [light], got %#v", got)
	}
}

func TestJobLifecycleUseCase_CreateAndConfirmDriverStatusRequest(t *testing.T) {
	repo := newLifecycleRepository()
	emitter := &lifecycleEmitter{}
	useCase := NewJobLifecycleUseCase(
		repo,
		emitter,
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:   "job-driver-status-1",
		Pickup:  domain.JobStop{Address: "Apapa Port"},
		Dropoff: domain.JobStop{Address: "Lekki Free Zone"},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}
	job, err = useCase.AssignJob(context.Background(), AssignJobCommand{
		JobID:       job.ID,
		VehicleID:   "vehicle-1",
		DriverID:    "driver-1",
		DriverPhone: "+2348010000001",
	})
	if err != nil {
		t.Fatalf("assign job failed: %v", err)
	}

	updatedJob, request, err := useCase.CreateDriverStatusRequest(context.Background(), CreateDriverStatusRequestCommand{
		JobID:           job.ID,
		DriverID:        "driver-1",
		DriverPhone:     "+2348010000001",
		Keyword:         "ENROUTE",
		RawMessage:      "ENROUTE",
		SourceMessageID: "wamid.inbound.1",
		Channel:         "whatsapp",
	})
	if err != nil {
		t.Fatalf("create driver status request failed: %v", err)
	}
	if request.RequestedStatus != domain.StatusEnRoute {
		t.Fatalf("expected en-route request, got %q", request.RequestedStatus)
	}
	if len(updatedJob.DriverStatusRequests) != 1 {
		t.Fatalf("expected one driver status request, got %d", len(updatedJob.DriverStatusRequests))
	}
	if updatedJob.Status != domain.StatusAssigned {
		t.Fatalf("expected job status to remain assigned until confirmation, got %q", updatedJob.Status)
	}

	confirmedJob, confirmedRequest, err := useCase.ConfirmDriverStatusRequest(context.Background(), ConfirmDriverStatusRequestCommand{
		JobID:       job.ID,
		RequestID:   request.ID,
		ConfirmedBy: "dispatcher-1",
	})
	if err != nil {
		t.Fatalf("confirm driver status request failed: %v", err)
	}
	if confirmedRequest.State != domain.DriverStatusRequestConfirmed {
		t.Fatalf("expected confirmed request state, got %q", confirmedRequest.State)
	}
	if confirmedJob.Status != domain.StatusEnRoute {
		t.Fatalf("expected confirmed job status en-route, got %q", confirmedJob.Status)
	}
	if len(emitter.commands) == 0 {
		t.Fatal("expected status change event to be emitted on confirmation")
	}
}

func TestJobLifecycleUseCase_RejectsDuplicatePendingDriverStatusRequest(t *testing.T) {
	repo := newLifecycleRepository()
	useCase := NewJobLifecycleUseCase(
		repo,
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:   "job-driver-status-2",
		Pickup:  domain.JobStop{Address: "Apapa Port"},
		Dropoff: domain.JobStop{Address: "Lekki Free Zone"},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}
	job, err = useCase.AssignJob(context.Background(), AssignJobCommand{
		JobID:       job.ID,
		VehicleID:   "vehicle-1",
		DriverID:    "driver-1",
		DriverPhone: "+2348010000001",
	})
	if err != nil {
		t.Fatalf("assign job failed: %v", err)
	}

	_, _, err = useCase.CreateDriverStatusRequest(context.Background(), CreateDriverStatusRequestCommand{
		JobID:           job.ID,
		DriverID:        "driver-1",
		DriverPhone:     "+2348010000001",
		Keyword:         "ENROUTE",
		RawMessage:      "ENROUTE",
		SourceMessageID: "wamid.inbound.2",
		Channel:         "whatsapp",
	})
	if err != nil {
		t.Fatalf("initial driver status request failed: %v", err)
	}

	_, _, err = useCase.CreateDriverStatusRequest(context.Background(), CreateDriverStatusRequestCommand{
		JobID:           job.ID,
		DriverID:        "driver-1",
		DriverPhone:     "+2348010000001",
		Keyword:         "ENROUTE",
		RawMessage:      "ENROUTE",
		SourceMessageID: "wamid.inbound.3",
		Channel:         "whatsapp",
	})
	if err == nil {
		t.Fatal("expected duplicate pending request to be rejected")
	}
}

func TestJobLifecycleUseCase_UpdateDetails_HappyPath(t *testing.T) {
	repo := newLifecycleRepository()
	useCase := NewJobLifecycleUseCase(
		repo,
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:     "job-update-1",
		AmountNgn: 1000,
		Pickup:    domain.JobStop{Address: "Apapa Port"},
		Dropoff:   domain.JobStop{Address: "Lekki Free Zone"},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	updatedJob, err := useCase.UpdateDetails(context.Background(), UpdateJobDetailsCommand{
		JobID:            job.ID,
		Pickup:           domain.JobStop{Address: "Ikeja"},
		Dropoff:          domain.JobStop{Address: "Yaba"},
		Stops:            []domain.JobStop{{Address: "Oshodi"}},
		CargoType:        "Furniture",
		CargoDescription: "Office chairs",
		AmountNgn:        5000,
	})
	if err != nil {
		t.Fatalf("update details failed: %v", err)
	}
	if updatedJob.Pickup.Address != "Ikeja" || updatedJob.Dropoff.Address != "Yaba" {
		t.Fatalf("expected pickup/dropoff to change, got %#v/%#v", updatedJob.Pickup, updatedJob.Dropoff)
	}
	if updatedJob.CargoType != "Furniture" || updatedJob.AmountNgn != 5000 {
		t.Fatalf("expected cargo/amount to change, got cargo=%q amount=%v", updatedJob.CargoType, updatedJob.AmountNgn)
	}
	if len(updatedJob.Stops) != 1 || updatedJob.Stops[0].Address != "Oshodi" {
		t.Fatalf("expected one stop Oshodi, got %#v", updatedJob.Stops)
	}

	saved, ok := repo.FindByTenantAndID("default", job.ID)
	if !ok {
		t.Fatal("expected job to be saved")
	}
	if saved.Pickup.Address != "Ikeja" || saved.AmountNgn != 5000 {
		t.Fatalf("expected saved job to reflect update, got %#v", saved)
	}
}

func TestJobLifecycleUseCase_UpdateDetails_NotFound(t *testing.T) {
	useCase := NewJobLifecycleUseCase(
		newLifecycleRepository(),
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	_, err := useCase.UpdateDetails(context.Background(), UpdateJobDetailsCommand{
		JobID:   "unknown-job",
		Pickup:  domain.JobStop{Address: "Ikeja"},
		Dropoff: domain.JobStop{Address: "Yaba"},
	})
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}

func TestJobLifecycleUseCase_UpdateDetails_CancelledJob(t *testing.T) {
	useCase := NewJobLifecycleUseCase(
		newLifecycleRepository(),
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:   "job-update-2",
		Pickup:  domain.JobStop{Address: "Apapa Port"},
		Dropoff: domain.JobStop{Address: "Lekki Free Zone"},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	_, err = useCase.CancelJob(context.Background(), CancelJobCommand{
		JobID:  job.ID,
		Reason: "customer request",
	})
	if err != nil {
		t.Fatalf("cancel job failed: %v", err)
	}

	_, err = useCase.UpdateDetails(context.Background(), UpdateJobDetailsCommand{
		JobID:   job.ID,
		Pickup:  domain.JobStop{Address: "Ikeja"},
		Dropoff: domain.JobStop{Address: "Yaba"},
	})
	if err == nil {
		t.Fatal("expected error updating details of a cancelled job")
	}
}

func TestJobLifecycleUseCase_DeleteJob_HappyPath(t *testing.T) {
	repo := newLifecycleRepository()
	useCase := NewJobLifecycleUseCase(
		repo,
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:   "job-delete-1",
		Pickup:  domain.JobStop{Address: "Apapa Port"},
		Dropoff: domain.JobStop{Address: "Lekki Free Zone"},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	if err := useCase.DeleteJob(context.Background(), DeleteJobCommand{JobID: job.ID}); err != nil {
		t.Fatalf("delete job failed: %v", err)
	}

	if _, ok := repo.FindByTenantAndID("default", job.ID); ok {
		t.Fatal("expected job to be deleted from repository")
	}
}

func TestJobLifecycleUseCase_DeleteJob_NotFound(t *testing.T) {
	repo := newLifecycleRepository()
	useCase := NewJobLifecycleUseCase(
		repo,
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	err := useCase.DeleteJob(context.Background(), DeleteJobCommand{JobID: "unknown-job"})
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
	if repo.deleteCalls != 0 {
		t.Fatalf("expected repository.Delete not to be called, got %d calls", repo.deleteCalls)
	}
}

func TestJobLifecycleUseCase_DeleteJob_TenantIsolation(t *testing.T) {
	repo := newLifecycleRepository()
	useCase := NewJobLifecycleUseCase(
		repo,
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		TenantID: "tenant-a",
		JobID:    "job-delete-2",
		Pickup:   domain.JobStop{Address: "Apapa Port"},
		Dropoff:  domain.JobStop{Address: "Lekki Free Zone"},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	err = useCase.DeleteJob(context.Background(), DeleteJobCommand{TenantID: "tenant-b", JobID: job.ID})
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound for cross-tenant delete, got %v", err)
	}
	if repo.deleteCalls != 0 {
		t.Fatalf("expected repository.Delete not to be called, got %d calls", repo.deleteCalls)
	}
	if _, ok := repo.FindByTenantAndID("tenant-a", job.ID); !ok {
		t.Fatal("expected tenant-a job to remain untouched")
	}
}

func TestJobLifecycleUseCase_SummarizeJobs_ExcludesCancelledFromTotalValue(t *testing.T) {
	useCase := NewJobLifecycleUseCase(
		newLifecycleRepository(),
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	jobOne, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:     "job-summary-1",
		AmountNgn: 1000,
		Pickup:    domain.JobStop{Address: "Apapa Port"},
		Dropoff:   domain.JobStop{Address: "Lekki Free Zone"},
	})
	if err != nil {
		t.Fatalf("create job one failed: %v", err)
	}
	_, err = useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:     "job-summary-2",
		AmountNgn: 2000,
		Pickup:    domain.JobStop{Address: "Ikeja"},
		Dropoff:   domain.JobStop{Address: "Yaba"},
	})
	if err != nil {
		t.Fatalf("create job two failed: %v", err)
	}
	jobThree, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:     "job-summary-3",
		AmountNgn: 4000,
		Pickup:    domain.JobStop{Address: "Ojota"},
		Dropoff:   domain.JobStop{Address: "Surulere"},
	})
	if err != nil {
		t.Fatalf("create job three failed: %v", err)
	}

	if _, err := useCase.CancelJob(context.Background(), CancelJobCommand{JobID: jobThree.ID, Reason: "duplicate"}); err != nil {
		t.Fatalf("cancel job three failed: %v", err)
	}

	summary, err := useCase.SummarizeJobs(context.Background(), "")
	if err != nil {
		t.Fatalf("summarize jobs failed: %v", err)
	}
	if summary.Total != 3 {
		t.Fatalf("expected total 3, got %d", summary.Total)
	}
	wantTotalValue := jobOne.AmountNgn + 2000.0
	if summary.TotalValueNgn != wantTotalValue {
		t.Fatalf("expected total value %v excluding cancelled job, got %v", wantTotalValue, summary.TotalValueNgn)
	}
}

func TestJobLifecycleUseCase_CreateJobWithStops(t *testing.T) {
	repo := newLifecycleRepository()
	useCase := NewJobLifecycleUseCase(
		repo,
		&lifecycleEmitter{},
		&lifecycleNotifier{},
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)

	stops := []domain.JobStop{
		{Address: "Ojota Junction"},
		{Address: "Maryland"},
	}
	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:   "job-stops-1",
		Pickup:  domain.JobStop{Address: "Apapa Port"},
		Dropoff: domain.JobStop{Address: "Lekki Free Zone"},
		Stops:   stops,
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}
	if len(job.Stops) != 2 || job.Stops[0].Address != "Ojota Junction" || job.Stops[1].Address != "Maryland" {
		t.Fatalf("expected returned job to have 2 stops, got %#v", job.Stops)
	}

	saved, ok := repo.FindByTenantAndID("default", job.ID)
	if !ok {
		t.Fatal("expected job to be saved")
	}
	if len(saved.Stops) != 2 || saved.Stops[0].Address != "Ojota Junction" || saved.Stops[1].Address != "Maryland" {
		t.Fatalf("expected saved job to have 2 stops, got %#v", saved.Stops)
	}
}

func TestJobLifecycleUseCase_AssignVehicleThenDriver(t *testing.T) {
	emitter := &lifecycleEmitter{}
	notifier := &lifecycleNotifier{}
	useCase := NewJobLifecycleUseCase(
		newLifecycleRepository(),
		emitter,
		notifier,
		lifecycleClock{now: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)},
		&lifecycleID{},
		"default",
	)
	job, err := useCase.CreateJob(context.Background(), CreateJobCommand{
		JobID:   "job-1",
		Pickup:  domain.JobStop{Address: "Apapa Port"},
		Dropoff: domain.JobStop{Address: "Lekki Free Zone"},
	})
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	// No driver to tell yet, so nothing is announced.
	job, err = useCase.AssignJob(context.Background(), AssignJobCommand{JobID: job.ID, VehicleID: "vehicle-1"})
	if err != nil {
		t.Fatalf("assign vehicle failed: %v", err)
	}
	if job.Status != domain.StatusCreated || notifier.calls != 0 || len(emitter.commands) != 0 {
		t.Fatalf("expected a silent vehicle-only assign, got status=%q notified=%d events=%d", job.Status, notifier.calls, len(emitter.commands))
	}

	job, err = useCase.AssignJob(context.Background(), AssignJobCommand{JobID: job.ID, DriverID: "driver-1"})
	if err != nil {
		t.Fatalf("assign driver failed: %v", err)
	}
	if job.Status != domain.StatusAssigned || job.AssignedVehicleID != "vehicle-1" {
		t.Fatalf("expected assigned with vehicle kept, got status=%q vehicle=%q", job.Status, job.AssignedVehicleID)
	}
	if notifier.calls != 1 || len(emitter.commands) != 1 {
		t.Fatalf("expected one notification and one event, got notified=%d events=%d", notifier.calls, len(emitter.commands))
	}
}
