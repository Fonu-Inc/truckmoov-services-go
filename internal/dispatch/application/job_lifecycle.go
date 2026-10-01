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
	Delete(tenantID, id string) error
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
	Pickup                  domain.JobStop
	Dropoff                 domain.JobStop
	Stops                   []domain.JobStop
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
}

var ErrDriverStatusRequestNotFound = errors.New("driver status request not found")

type JobLifecycleUseCase struct {
	repository    JobRepository
	statusEmitter JobStatusChangedEmitter
	notifier      DriverInstructionNotifier
	clock         Clock
	idGenerator   IDGenerator
	defaultTenant string
	estimator     RouteEstimator
}

// WithRouteEstimator enables punctuality capture on delivery. Optional: without
// it a delivered job still records deadline adherence, just with no traffic
// allowance.
func (u *JobLifecycleUseCase) WithRouteEstimator(estimator RouteEstimator) *JobLifecycleUseCase {
	u.estimator = estimator
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
		AmountNgn:               command.AmountNgn,
		Deadline:                command.Deadline,
		RequiredVehicleType:     command.RequiredVehicleType,
		LoadWeightKg:            command.LoadWeightKg,
		LoadVolumeM3:            command.LoadVolumeM3,
		EstimatedServiceMinutes: command.EstimatedServiceMinutes,
		Priority:                priority,
		TimeWindow:              timeWindow,
		Status:                  status,
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

	previousStatus := job.Status
	updatedJob, err := job.Assign(command.DriverID, command.DriverPhone, command.VehicleID, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}

	if err := u.repository.Save(updatedJob); err != nil {
		return domain.Job{}, fmt.Errorf("save assigned job: %w", err)
	}
	if updatedJob.AssignedDriverID == "" {
		return updatedJob, nil
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

func (u *JobLifecycleUseCase) SummarizeJobs(ctx context.Context, tenantID string) (JobStatusSummary, error) {
	jobs, err := u.ListJobs(ctx, tenantID, ListJobsFilter{})
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
