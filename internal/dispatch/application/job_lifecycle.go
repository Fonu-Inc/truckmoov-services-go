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

var (
	ErrJobNotFound      = domain.ErrJobNotFound
	ErrJobAlreadyExists = errors.New("job already exists")
)

type JobRepository interface {
	Save(job domain.Job) error
	FindByTenantAndID(tenantID, id string) (domain.Job, error)
	FindByTenant(tenantID string) ([]domain.Job, error)
	Delete(tenantID, id string) error
}

type JobStatusChangedEmitter interface {
	Execute(ctx context.Context, command PublishJobStatusChangedCommand) (PublishJobStatusChangedResult, error)
}

// DriverInstructionNotifier asks the notification service to message the
// job's assigned driver. It only queues the request: whether the message
// reached the driver is reported back through RecordDriverInstructionOutcome.
type DriverInstructionNotifier interface {
	NotifyJobInstruction(ctx context.Context, job domain.Job, message string) error
}

type CreateJobCommand struct {
	TenantID                string
	JobID                   string
	Reference               string
	Pickup                  domain.JobStop
	Dropoff                 domain.JobStop
	Stops                   []domain.JobStop
	CargoType               string
	CargoDescription        string
	Cargo                   domain.JobCargoDetails
	InitialNote             string
	AmountNgn               float64
	Deadline                *time.Time
	RequiredVehicleType     string
	LoadWeightKg            float64
	LoadVolumeM3            float64
	EstimatedServiceMinutes int
	Priority                string
	TimeWindow              domain.JobTimeWindow
	CreatedBy               string
}

type UpdateJobSchedulingCommand struct {
	TenantID   string
	JobID      string
	Priority   *string
	TimeWindow *domain.JobTimeWindow
}

type AssignJobCommand struct {
	TenantID    string
	JobID       string
	DriverID    string
	DriverPhone string
	VehicleID   string
	Reason      string
}

type CancelJobCommand struct {
	TenantID string
	JobID    string
	Reason   string
}

type DeleteJobCommand struct {
	TenantID string
	JobID    string
}

type UpdateJobDetailsCommand struct {
	TenantID string
	JobID    string
	Patch    domain.JobDetailsPatch
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

type DriverInstructionOutcome string

const (
	DriverInstructionSent    DriverInstructionOutcome = "sent"
	DriverInstructionFailed  DriverInstructionOutcome = "failed"
	DriverInstructionNotSent DriverInstructionOutcome = "not_sent"
)

type RecordDriverInstructionOutcomeCommand struct {
	TenantID string
	JobID    string
	DriverID string
	Outcome  DriverInstructionOutcome
	Channel  string
	Reason   string
}

type CreateDriverStatusRequestCommand struct {
	TenantID        string
	JobID           string
	DriverID        string
	DriverPhone     string
	Keyword         string
	RawMessage      string
	SourceMessageID string
	Channel         string
}

type ConfirmDriverStatusRequestCommand struct {
	TenantID    string
	JobID       string
	RequestID   string
	ConfirmedBy string
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
	// Optional range/date filters for the Dispatch Center "Only show
	// shipments by..." panel. Weight bounds are in kilograms (the handler
	// converts the tons the UI submits). nil means "not filtering on it".
	WeightFromKg *float64
	WeightToKg   *float64
	AmountFrom   *float64
	AmountTo     *float64
	Deadline     *time.Time
	Scope        *JobScope
}

var (
	ErrDriverStatusRequestNotFound     = errors.New("driver status request not found")
	ErrDriverInstructionOutcomeInvalid = errors.New("outcome must be sent, failed or not_sent")
)

type JobLifecycleUseCase struct {
	repository    JobRepository
	statusEmitter JobStatusChangedEmitter
	notifier      DriverInstructionNotifier
	clock         Clock
	idGenerator   IDGenerator
	defaultTenant string
	estimator     RouteEstimator
	fleet         FleetDirectory
}

// WithRouteEstimator enables punctuality capture on delivery. Optional: without
// it a delivered job still records deadline adherence, just with no traffic
// allowance.
func (u *JobLifecycleUseCase) WithRouteEstimator(estimator RouteEstimator) *JobLifecycleUseCase {
	u.estimator = estimator
	return u
}

// WithFleetDirectory makes assignment refuse a driver or vehicle fleet does
// not know in the job's tenant.
func (u *JobLifecycleUseCase) WithFleetDirectory(fleet FleetDirectory) *JobLifecycleUseCase {
	u.fleet = fleet
	return u
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

	jobID, err := u.newJobID(tenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
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

	job, err := domain.NewJob(domain.Job{
		ID:                      jobID,
		Reference:               reference,
		TenantID:                tenantID,
		Pickup:                  command.Pickup,
		Dropoff:                 command.Dropoff,
		Stops:                   command.Stops,
		CargoType:               command.CargoType,
		CargoDescription:        command.CargoDescription,
		JobCargoDetails:         command.Cargo,
		AmountNgn:               command.AmountNgn,
		Deadline:                command.Deadline,
		RequiredVehicleType:     command.RequiredVehicleType,
		LoadWeightKg:            command.LoadWeightKg,
		LoadVolumeM3:            command.LoadVolumeM3,
		EstimatedServiceMinutes: command.EstimatedServiceMinutes,
		Priority:                priority,
		TimeWindow:              timeWindow,
		Status:                  status,
		CreatedBy:               &command.CreatedBy,
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

	return job, nil
}

// newJobID refuses a caller-chosen id already in use: saving would overwrite
// that job, its history and its creator.
func (u *JobLifecycleUseCase) newJobID(tenantID, requested string) (string, error) {
	jobID := strings.TrimSpace(requested)
	if jobID == "" {
		return u.idGenerator.New(), nil
	}
	_, err := u.repository.FindByTenantAndID(tenantID, jobID)
	if err == nil {
		return "", ErrJobAlreadyExists
	}
	if !errors.Is(err, ErrJobNotFound) {
		return "", fmt.Errorf("check job id: %w", err)
	}
	return jobID, nil
}

func (u *JobLifecycleUseCase) UpdateScheduling(_ context.Context, command UpdateJobSchedulingCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	patch := domain.SchedulingPatch{TimeWindow: command.TimeWindow}
	if command.Priority != nil {
		priority, err := domain.NormalizePriority(*command.Priority)
		if err != nil {
			return domain.Job{}, err
		}
		patch.Priority = &priority
	}

	updatedJob, err := job.SetScheduling(patch, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save job scheduling: %w", err)
	}
	return updatedJob, nil
}

func (u *JobLifecycleUseCase) UpdateDetails(_ context.Context, command UpdateJobDetailsCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	updatedJob, err := job.PatchDetails(command.Patch, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save job details: %w", err)
	}
	return updatedJob, nil
}

func (u *JobLifecycleUseCase) DeleteJob(_ context.Context, command DeleteJobCommand) error {
	if _, err := u.findJobForTenant(command.TenantID, command.JobID); err != nil {
		return err
	}
	resolvedTenant := u.resolveTenant(command.TenantID)
	if err := u.repository.Delete(resolvedTenant, strings.TrimSpace(command.JobID)); err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	return nil
}

func (u *JobLifecycleUseCase) AssignJob(ctx context.Context, command AssignJobCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}

	updatedJob, err := job.Assign(command.DriverID, command.DriverPhone, command.VehicleID, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if err := verifyAssignees(ctx, u.fleet, job.TenantID, command.DriverID, command.VehicleID); err != nil {
		return domain.Job{}, err
	}
	if err := verifyMaintenance(ctx, u.fleet, job.TenantID, command.VehicleID, job.AssignedVehicleID); err != nil {
		return domain.Job{}, err
	}

	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save assigned job: %w", err)
	}
	return u.announceAssignment(ctx, job, updatedJob, command.Reason), nil
}

// announceAssignment emits the status event when the status or the driver
// changed, and instructs the driver only when they are new to the job: the
// instruction says nothing about the vehicle.
func (u *JobLifecycleUseCase) announceAssignment(ctx context.Context, before, after domain.Job, reason string) domain.Job {
	driverChanged := after.DriverChangedFrom(before)
	if after.Status != before.Status || driverChanged {
		u.emitStatusChanged(ctx, PublishJobStatusChangedCommand{
			TenantID:          after.TenantID,
			JobID:             after.ID,
			PreviousStatus:    string(before.Status),
			NewStatus:         string(after.Status),
			AssignedDriverID:  after.AssignedDriverID,
			AssignedVehicleID: after.AssignedVehicleID,
			Reason:            strings.TrimSpace(reason),
		})
	}
	if !driverChanged {
		return after
	}
	return u.instructDriver(ctx, after)
}

// instructDriver is best effort: assignment never fails because messaging is
// unavailable. Nothing is written to the timeline here, because nothing has
// reached the driver yet; the notification service reports the real outcome.
func (u *JobLifecycleUseCase) instructDriver(ctx context.Context, job domain.Job) domain.Job {
	if u.notifier == nil {
		return job
	}
	if err := u.notifier.NotifyJobInstruction(ctx, job, buildDriverInstructionMessage(job)); err != nil {
		return u.saveInstructionOutcome(job, job.MarkDriverInstructionNotSent("whatsapp", "the message could not be queued", u.clock.Now(), u.idGenerator.New()))
	}
	return job
}

// ResendDriverInstruction asks again for the assigned driver to be messaged,
// for when the first attempt failed or never had a number to send to. Only an
// assigned job qualifies: after that the driver already has the job.
func (u *JobLifecycleUseCase) ResendDriverInstruction(ctx context.Context, tenantID, jobID string) (domain.Job, error) {
	job, err := u.findJobForTenant(tenantID, jobID)
	if err != nil {
		return domain.Job{}, err
	}
	if job.Status != domain.StatusAssigned || job.AssignedDriverID == "" {
		return domain.Job{}, ErrJobInstructionStatusNotSendable
	}
	return u.instructDriver(ctx, job), nil
}

func (u *JobLifecycleUseCase) saveInstructionOutcome(fallback, updated domain.Job) domain.Job {
	if err := u.repository.Save(updated); err != nil {
		return fallback
	}
	return updated
}

// RecordDriverInstructionOutcome writes what happened to a driver instruction
// on the timeline. An outcome for a driver who is no longer assigned is
// refused, so a late report cannot decorate the new driver's history.
func (u *JobLifecycleUseCase) RecordDriverInstructionOutcome(_ context.Context, command RecordDriverInstructionOutcomeCommand) (domain.Job, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, err
	}
	if strings.TrimSpace(command.DriverID) != job.AssignedDriverID {
		return domain.Job{}, ErrJobInstructionDriverMismatch
	}

	now, timelineID := u.clock.Now(), u.idGenerator.New()
	var updated domain.Job
	switch command.Outcome {
	case DriverInstructionSent:
		updated = job.MarkDriverInstructionSent(command.Channel, now, timelineID)
	case DriverInstructionFailed:
		updated = job.MarkDriverInstructionFailed(command.Channel, command.Reason, now, timelineID)
	case DriverInstructionNotSent:
		updated = job.MarkDriverInstructionNotSent(command.Channel, command.Reason, now, timelineID)
	default:
		return domain.Job{}, ErrDriverInstructionOutcomeInvalid
	}
	if err := u.repository.Save(updated); err != nil {
		return domain.Job{}, fmt.Errorf("save driver instruction outcome: %w", err)
	}
	return updated, nil
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
	completedAt := u.clock.Now()
	updatedJob, err := job.UpdateStatus(status, completedAt, u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if updatedJob.Status == domain.StatusDelivered && updatedJob.Punctuality == nil {
		updatedJob.Punctuality = BuildJobPunctuality(ctx, updatedJob, completedAt, u.estimator)
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

func (u *JobLifecycleUseCase) CreateDriverStatusRequest(_ context.Context, command CreateDriverStatusRequestCommand) (domain.Job, domain.DriverStatusRequest, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, domain.DriverStatusRequest{}, err
	}

	requestedStatus, err := parseDriverStatusKeyword(command.Keyword)
	if err != nil {
		return domain.Job{}, domain.DriverStatusRequest{}, err
	}

	updatedJob, request, err := job.ReportDriverStatusRequest(
		requestedStatus,
		command.DriverID,
		command.DriverPhone,
		command.Keyword,
		command.RawMessage,
		command.SourceMessageID,
		firstNonEmptyDispatchValue(command.Channel, "whatsapp"),
		u.clock.Now(),
		u.idGenerator.New(),
		u.idGenerator.New(),
	)
	if err != nil {
		return domain.Job{}, domain.DriverStatusRequest{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, domain.DriverStatusRequest{}, fmt.Errorf("save driver status request: %w", err)
	}

	return updatedJob, request, nil
}

func (u *JobLifecycleUseCase) ConfirmDriverStatusRequest(ctx context.Context, command ConfirmDriverStatusRequestCommand) (domain.Job, domain.DriverStatusRequest, error) {
	job, err := u.findJobForTenant(command.TenantID, command.JobID)
	if err != nil {
		return domain.Job{}, domain.DriverStatusRequest{}, err
	}

	previousStatus := job.Status
	updatedJob, request, err := job.ConfirmDriverStatusRequest(
		command.RequestID,
		command.ConfirmedBy,
		u.clock.Now(),
		u.idGenerator.New(),
		u.idGenerator.New(),
	)
	if err != nil {
		if strings.Contains(err.Error(), "driver status request not found") {
			return domain.Job{}, domain.DriverStatusRequest{}, ErrDriverStatusRequestNotFound
		}
		return domain.Job{}, domain.DriverStatusRequest{}, err
	}
	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, domain.DriverStatusRequest{}, fmt.Errorf("save confirmed driver status request: %w", err)
	}

	u.emitStatusChanged(ctx, PublishJobStatusChangedCommand{
		TenantID:          updatedJob.TenantID,
		JobID:             updatedJob.ID,
		PreviousStatus:    string(previousStatus),
		NewStatus:         string(updatedJob.Status),
		AssignedDriverID:  updatedJob.AssignedDriverID,
		AssignedVehicleID: updatedJob.AssignedVehicleID,
	})

	return updatedJob, request, nil
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
	tenantJobs, err := u.repository.FindByTenant(u.resolveTenant(tenantID))
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	jobs := append([]domain.Job{}, tenantJobs...)

	statusMatcher, err := buildJobStatusMatcher(filter.Status)
	if err != nil {
		return nil, err
	}

	query := strings.ToLower(strings.TrimSpace(filter.Query))
	driverID := strings.TrimSpace(filter.DriverID)
	vehicleID := strings.TrimSpace(filter.VehicleID)

	filtered := make([]domain.Job, 0, len(jobs))
	for _, job := range jobs {
		if !filter.Scope.Allows(job) {
			continue
		}
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
		if filter.WeightFromKg != nil && job.LoadWeightKg < *filter.WeightFromKg {
			continue
		}
		if filter.WeightToKg != nil && job.LoadWeightKg > *filter.WeightToKg {
			continue
		}
		if filter.AmountFrom != nil && job.AmountNgn < *filter.AmountFrom {
			continue
		}
		if filter.AmountTo != nil && job.AmountNgn > *filter.AmountTo {
			continue
		}
		if filter.Deadline != nil && !jobDeadlineOnDate(job, *filter.Deadline) {
			continue
		}
		filtered = append(filtered, job)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	return filtered, nil
}

// jobDeadlineOnDate reports whether the job's deadline falls on the given
// calendar day (compared in UTC). Jobs without a deadline never match.
func jobDeadlineOnDate(job domain.Job, date time.Time) bool {
	if job.Deadline == nil {
		return false
	}
	d := job.Deadline.UTC()
	return d.Year() == date.Year() && d.Month() == date.Month() && d.Day() == date.Day()
}

type JobStatusSummary struct {
	Total         int            `json:"total"`
	ByStatus      map[string]int `json:"by_status"`
	TotalValueNgn float64        `json:"total_value_ngn"`
}

func (u *JobLifecycleUseCase) SummarizeJobs(ctx context.Context, tenantID string, scope *JobScope) (JobStatusSummary, error) {
	jobs, err := u.ListJobs(ctx, tenantID, ListJobsFilter{Scope: scope})
	if err != nil {
		return JobStatusSummary{}, err
	}
	byStatus := map[string]int{
		string(domain.StatusCreated):   0,
		string(domain.StatusAssigned):  0,
		string(domain.StatusEnRoute):   0,
		string(domain.StatusArrived):   0,
		string(domain.StatusLoaded):    0,
		string(domain.StatusDelivered): 0,
		string(domain.StatusCancelled): 0,
	}
	var totalValueNgn float64
	for _, job := range jobs {
		byStatus[string(job.Status)]++
		if job.Status != domain.StatusCancelled {
			totalValueNgn += job.AmountNgn
		}
	}
	return JobStatusSummary{Total: len(jobs), ByStatus: byStatus, TotalValueNgn: totalValueNgn}, nil
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
	return u.repository.FindByTenantAndID(resolvedTenant, strings.TrimSpace(jobID))
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
			return current == domain.StatusEnRoute ||
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

func parseDriverStatusKeyword(raw string) (domain.JobStatus, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.NewReplacer("_", "", "-", "", " ", "").Replace(normalized)
	switch normalized {
	case "enroute":
		return domain.StatusEnRoute, nil
	case "arrived":
		return domain.StatusArrived, nil
	case "loaded":
		return domain.StatusLoaded, nil
	case "delivered":
		return domain.StatusDelivered, nil
	default:
		return "", fmt.Errorf("unsupported driver status keyword %q", raw)
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
