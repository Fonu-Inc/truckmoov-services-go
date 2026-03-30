package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

var ErrJobNotFound = errors.New("job not found")

type JobRepository interface {
	Save(job domain.Job) error
	FindByTenantAndID(tenantID, id string) (domain.Job, bool)
	FindByTenant(tenantID string) []domain.Job
}

type JobStatusChangedEmitter interface {
	Execute(ctx context.Context, command PublishJobStatusChangedCommand) (PublishJobStatusChangedResult, error)
}

type DriverInstructionNotifier interface {
	NotifyJobInstruction(
		ctx context.Context,
		tenantID, jobID, driverID, message string,
	) error
}

type CreateJobCommand struct {
	TenantID                string
	JobID                   string
	Reference               string
	VehicleID               string
	DriverID                string
	Pickup                  domain.JobStop
	Dropoff                 domain.JobStop
	CargoType               string
	CargoDescription        string
	InitialNote             string
	AmountNgn               float64
	Deadline                *time.Time
	RequiredVehicleType     string
	LoadWeightKg            float64
	LoadVolumeM3            float64
	EstimatedServiceMinutes int
	Priority                string
	TimeWindow              domain.JobTimeWindow
}

type UpdateJobSchedulingCommand struct {
	TenantID   string
	JobID      string
	Priority   string
	TimeWindow domain.JobTimeWindow
}

type AssignJobCommand struct {
	TenantID  string
	JobID     string
	DriverID  string
	VehicleID string
	Reason    string
}

type CancelJobCommand struct {
	TenantID string
	JobID    string
	Reason   string
}

type AcknowledgeJobCommand struct {
	TenantID string
	JobID    string
	DriverID string
}

type UpdateJobStatusCommand struct {
	TenantID string
	JobID    string
	Status   string
}

type AddJobPODAttachmentCommand struct {
	TenantID   string
	JobID      string
	Kind       string
	URL        string
	UploadedBy string
}

type AddJobNoteCommand struct {
	TenantID string
	JobID    string
	Text     string
	AuthorID string
}

type ListJobsFilter struct {
	Status    string
	Query     string
	DriverID  string
	VehicleID string
}

type JobLifecycleUseCase struct {
	repository    JobRepository
	statusEmitter JobStatusChangedEmitter
	notifier      DriverInstructionNotifier
	clock         Clock
	idGenerator   IDGenerator
	defaultTenant string
}

func NewJobLifecycleUseCase(
	repository JobRepository,
	statusEmitter JobStatusChangedEmitter,
	notifier DriverInstructionNotifier,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
) *JobLifecycleUseCase {
	return &JobLifecycleUseCase{
		repository:    repository,
		statusEmitter: statusEmitter,
		notifier:      notifier,
		clock:         clock,
		idGenerator:   idGenerator,
		defaultTenant: defaultTenant,
	}
}

func (u *JobLifecycleUseCase) CreateJob(ctx context.Context, command CreateJobCommand) (domain.Job, error) {
	now := u.clock.Now().UTC()
	tenantID := u.resolveTenant(command.TenantID)
	priority, err := domain.NormalizePriority(command.Priority)
	if err != nil {
		return domain.Job{}, err
	}

	jobID := strings.TrimSpace(command.JobID)
	if jobID == "" {
		jobID = u.idGenerator.New()
	}
	reference := strings.TrimSpace(command.Reference)
	if reference == "" {
		reference = jobID
	}

	timeWindow := command.TimeWindow
	if timeWindow.DropoffEnd == nil && command.Deadline != nil {
		deadlineUTC := command.Deadline.UTC()
		timeWindow.DropoffEnd = &deadlineUTC
	}

	assignedVehicleID := strings.TrimSpace(command.VehicleID)
	assignedDriverID := strings.TrimSpace(command.DriverID)
	if assignedDriverID != "" && assignedVehicleID == "" {
		return domain.Job{}, errors.New("vehicle id is required when driver id is provided")
	}

	status := domain.StatusCreated
	timeline := []domain.JobTimelineEntry{
		{
			ID:         u.idGenerator.New(),
			Type:       "job_created",
			Summary:    "Job created with pickup/dropoff details",
			OccurredAt: now,
		},
	}

	notes := make([]domain.JobNote, 0)
	if initialNote := strings.TrimSpace(command.InitialNote); initialNote != "" {
		noteID := u.idGenerator.New()
		notes = append(notes, domain.JobNote{
			ID:        noteID,
			Text:      initialNote,
			CreatedAt: now,
		})
		timeline = append(timeline, domain.JobTimelineEntry{
			ID:         u.idGenerator.New(),
			Type:       "job_note_added",
			Summary:    "Dispatcher note added to job",
			OccurredAt: now,
			Metadata: map[string]string{
				"note_id": noteID,
			},
		})
	}

	if assignedVehicleID != "" && assignedDriverID != "" {
		status = domain.StatusAssigned
		timeline = append(timeline, domain.JobTimelineEntry{
			ID:         u.idGenerator.New(),
			Type:       "job_assigned",
			Summary:    "Job assigned to driver/vehicle",
			OccurredAt: now,
			Metadata: map[string]string{
				"driver_id":  assignedDriverID,
				"vehicle_id": assignedVehicleID,
			},
		})
	}

	job, err := domain.NewJob(domain.Job{
		ID:                      jobID,
		Reference:               reference,
		TenantID:                tenantID,
		Pickup:                  command.Pickup,
		Dropoff:                 command.Dropoff,
		CargoType:               command.CargoType,
		CargoDescription:        command.CargoDescription,
		AmountNgn:               command.AmountNgn,
		Deadline:                command.Deadline,
		RequiredVehicleType:     command.RequiredVehicleType,
		LoadWeightKg:            command.LoadWeightKg,
		LoadVolumeM3:            command.LoadVolumeM3,
		EstimatedServiceMinutes: command.EstimatedServiceMinutes,
		Priority:                priority,
		TimeWindow:              timeWindow,
		Status:                  status,
		AssignedDriverID:        assignedDriverID,
		AssignedVehicleID:       assignedVehicleID,
		CreatedAt:               now,
		UpdatedAt:               now,
		Timeline:                timeline,
		Notes:                   notes,
		PODAttachments:          make([]domain.JobPODAttachment, 0),
	})
	if err != nil {
		return domain.Job{}, err
	}

	if err := u.repository.Save(job); err != nil {
		return domain.Job{}, fmt.Errorf("save job: %w", err)
	}

	// The original modal is a create-and-dispatch flow, so when both selections are supplied
	// we emit the same side effects that the explicit assign endpoint would have triggered.
	if assignedVehicleID != "" && assignedDriverID != "" {
		u.emitStatusChanged(ctx, PublishJobStatusChangedCommand{
			TenantID:          job.TenantID,
			JobID:             job.ID,
			PreviousStatus:    string(domain.StatusCreated),
			NewStatus:         string(job.Status),
			AssignedDriverID:  job.AssignedDriverID,
			AssignedVehicleID: job.AssignedVehicleID,
		})

		message := buildDriverInstructionMessage(job)
		if notifyErr := u.notifyDriverInstruction(
			ctx,
			job.TenantID,
			job.ID,
			job.AssignedDriverID,
			message,
		); notifyErr == nil {
			job = job.MarkDriverInstructionSent("whatsapp", u.clock.Now(), u.idGenerator.New())
			_ = u.repository.Save(job)
		}
	}

	return job, nil
}

func (u *JobLifecycleUseCase) UpdateScheduling(_ context.Context, command UpdateJobSchedulingCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	priority, err := domain.NormalizePriority(command.Priority)
	if err != nil {
		return domain.Job{}, err
	}

	updatedJob, err := job.SetScheduling(priority, command.TimeWindow, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save job scheduling: %w", err)
	}
	return updatedJob, nil
}

func (u *JobLifecycleUseCase) AssignJob(ctx context.Context, command AssignJobCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	previousStatus := job.Status
	updatedJob, err := job.Assign(command.DriverID, command.VehicleID, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}

	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save assigned job: %w", err)
	}

	u.emitStatusChanged(ctx, PublishJobStatusChangedCommand{
		TenantID:          updatedJob.TenantID,
		JobID:             updatedJob.ID,
		PreviousStatus:    string(previousStatus),
		NewStatus:         string(updatedJob.Status),
		AssignedDriverID:  updatedJob.AssignedDriverID,
		AssignedVehicleID: updatedJob.AssignedVehicleID,
		Reason:            strings.TrimSpace(command.Reason),
	})

	message := buildDriverInstructionMessage(updatedJob)
	if notifyErr := u.notifyDriverInstruction(
		ctx,
		updatedJob.TenantID,
		updatedJob.ID,
		updatedJob.AssignedDriverID,
		message,
	); notifyErr == nil {
		// Keep this as best effort so assignment never fails because messaging is unavailable.
		updatedJob = updatedJob.MarkDriverInstructionSent("whatsapp", u.clock.Now(), u.idGenerator.New())
		_ = u.repository.Save(updatedJob)
	}

	return updatedJob, nil
}

func (u *JobLifecycleUseCase) CancelJob(ctx context.Context, command CancelJobCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	previousStatus := job.Status
	updatedJob, err := job.Cancel(command.Reason, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save cancelled job: %w", err)
	}

	u.emitStatusChanged(ctx, PublishJobStatusChangedCommand{
		TenantID:          updatedJob.TenantID,
		JobID:             updatedJob.ID,
		PreviousStatus:    string(previousStatus),
		NewStatus:         string(updatedJob.Status),
		AssignedDriverID:  updatedJob.AssignedDriverID,
		AssignedVehicleID: updatedJob.AssignedVehicleID,
		Reason:            updatedJob.CancellationReason,
	})

	return updatedJob, nil
}

func (u *JobLifecycleUseCase) AcknowledgeJob(_ context.Context, command AcknowledgeJobCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	updatedJob, err := job.Acknowledge(command.DriverID, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save acknowledged job: %w", err)
	}

	return updatedJob, nil
}

func (u *JobLifecycleUseCase) UpdateJobStatus(ctx context.Context, command UpdateJobStatusCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	status, err := domain.NormalizeStatus(command.Status)
	if err != nil {
		return domain.Job{}, err
	}

	previousStatus := job.Status
	updatedJob, err := job.UpdateStatus(status, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save job status: %w", err)
	}

	u.emitStatusChanged(ctx, PublishJobStatusChangedCommand{
		TenantID:          updatedJob.TenantID,
		JobID:             updatedJob.ID,
		PreviousStatus:    string(previousStatus),
		NewStatus:         string(updatedJob.Status),
		AssignedDriverID:  updatedJob.AssignedDriverID,
		AssignedVehicleID: updatedJob.AssignedVehicleID,
	})

	return updatedJob, nil
}

func (u *JobLifecycleUseCase) AddPODAttachment(_ context.Context, command AddJobPODAttachmentCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	updatedJob, err := job.AddPODAttachment(
		domain.JobPODAttachment{
			ID:         u.idGenerator.New(),
			Kind:       command.Kind,
			URL:        command.URL,
			UploadedBy: command.UploadedBy,
		},
		u.clock.Now(),
		u.idGenerator.New(),
	)
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save pod attachment: %w", err)
	}
	return updatedJob, nil
}

func (u *JobLifecycleUseCase) AddNote(_ context.Context, command AddJobNoteCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	updatedJob, err := job.AddNote(
		domain.JobNote{
			ID:       u.idGenerator.New(),
			Text:     command.Text,
			AuthorID: command.AuthorID,
		},
		u.clock.Now(),
		u.idGenerator.New(),
	)
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save note: %w", err)
	}
	return updatedJob, nil
}

func (u *JobLifecycleUseCase) GetJob(_ context.Context, tenantID, jobID string) (domain.Job, error) {
	return u.findJobForTenant(tenantID, jobID)
}

func (u *JobLifecycleUseCase) ListJobs(_ context.Context, tenantID string, filter ListJobsFilter) ([]domain.Job, error) {
	jobs := append([]domain.Job{}, u.repository.FindByTenant(u.resolveTenant(tenantID))...)

	statusMatcher, err := buildJobStatusMatcher(filter.Status)
	if err != nil {
		return nil, err
	}

	query := strings.ToLower(strings.TrimSpace(filter.Query))
	driverID := strings.TrimSpace(filter.DriverID)
	vehicleID := strings.TrimSpace(filter.VehicleID)

	filtered := make([]domain.Job, 0, len(jobs))
	for _, job := range jobs {
		if statusMatcher != nil && !statusMatcher(job.Status) {
			continue
		}
		if driverID != "" && job.AssignedDriverID != driverID {
			continue
		}
		if vehicleID != "" && job.AssignedVehicleID != vehicleID {
			continue
		}
		if query != "" && !jobMatchesQuery(job, query) {
			continue
		}
		filtered = append(filtered, job)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	return filtered, nil
}

func (u *JobLifecycleUseCase) GetTimeline(_ context.Context, tenantID, jobID string) ([]domain.JobTimelineEntry, error) {
	job, err := u.findJobForTenant(tenantID, jobID)
	if err != nil {
		return nil, err
	}

	entries := append([]domain.JobTimelineEntry{}, job.Timeline...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].OccurredAt.Before(entries[j].OccurredAt)
	})

	return entries, nil
}

func (u *JobLifecycleUseCase) findJobForTenant(tenantID, jobID string) (domain.Job, error) {
	resolvedTenant := u.resolveTenant(tenantID)
	job, ok := u.repository.FindByTenantAndID(resolvedTenant, strings.TrimSpace(jobID))
	if !ok {
		return domain.Job{}, ErrJobNotFound
	}

	return job, nil
}

func (u *JobLifecycleUseCase) resolveTenant(tenantID string) string {
	resolved := strings.TrimSpace(tenantID)
	if resolved == "" {
		return u.defaultTenant
	}
	return resolved
}

func (u *JobLifecycleUseCase) emitStatusChanged(ctx context.Context, command PublishJobStatusChangedCommand) {
	if u.statusEmitter == nil {
		return
	}
	_, _ = u.statusEmitter.Execute(ctx, command)
}

func (u *JobLifecycleUseCase) notifyDriverInstruction(
	ctx context.Context,
	tenantID, jobID, driverID, message string,
) error {
	if u.notifier == nil {
		return nil
	}
	return u.notifier.NotifyJobInstruction(
		ctx,
		tenantID,
		jobID,
		driverID,
		message,
	)
}

func buildDriverInstructionMessage(job domain.Job) string {
	jobIdentifier := job.ID
	if strings.TrimSpace(job.Reference) != "" {
		jobIdentifier = job.Reference
	}
	return fmt.Sprintf(
		"New job %s assigned. Pickup: %s. Dropoff: %s. Priority: %s.",
		jobIdentifier,
		job.Pickup.Address,
		job.Dropoff.Address,
		job.Priority,
	)
}

func buildJobStatusMatcher(raw string) (func(domain.JobStatus) bool, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "", "all":
		return nil, nil
	case "pending":
		return func(current domain.JobStatus) bool { return current == domain.StatusCreated }, nil
	case "in_progress", "in-progress":
		return func(current domain.JobStatus) bool {
			return current == domain.StatusAssigned ||
				current == domain.StatusEnRoute ||
				current == domain.StatusArrived ||
				current == domain.StatusLoaded
		}, nil
	case "completed":
		return func(current domain.JobStatus) bool { return current == domain.StatusDelivered }, nil
	default:
		normalized, err := domain.NormalizeStatus(status)
		if err != nil {
			return nil, err
		}
		return func(current domain.JobStatus) bool { return current == normalized }, nil
	}
}

func jobMatchesQuery(job domain.Job, query string) bool {
	for _, field := range []string{
		job.ID,
		job.Reference,
		job.Pickup.Address,
		job.Dropoff.Address,
		job.CargoType,
		job.CargoDescription,
		job.AssignedDriverID,
		job.AssignedVehicleID,
	} {
		if strings.Contains(strings.ToLower(strings.TrimSpace(field)), query) {
			return true
		}
	}
	return false
}
