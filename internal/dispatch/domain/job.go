package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type JobPriority string

const (
	PriorityLow    JobPriority = "low"
	PriorityNormal JobPriority = "normal"
	PriorityHigh   JobPriority = "high"
	PriorityUrgent JobPriority = "urgent"
	// Deprecated alias kept so legacy code paths still compile while the
	// persisted and returned value stays aligned with the frontend vocabulary.
	PriorityCritical JobPriority = PriorityUrgent
)

type JobStatus string

const (
	StatusCreated   JobStatus = "created"
	StatusAssigned  JobStatus = "assigned"
	StatusEnRoute   JobStatus = "en-route"
	StatusArrived   JobStatus = "arrived"
	StatusLoaded    JobStatus = "loaded"
	StatusDelivered JobStatus = "delivered"
	StatusCancelled JobStatus = "cancelled"
)

type JobStop struct {
	Address      string   `json:"address"`
	ContactName  string   `json:"contact_name,omitempty"`
	ContactPhone string   `json:"contact_phone,omitempty"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
	Notes        string   `json:"notes,omitempty"`
}

type JobTimeWindow struct {
	PickupStart  *time.Time `json:"pickup_start,omitempty"`
	PickupEnd    *time.Time `json:"pickup_end,omitempty"`
	DropoffStart *time.Time `json:"dropoff_start,omitempty"`
	DropoffEnd   *time.Time `json:"dropoff_end,omitempty"`
}

type JobNote struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	AuthorID  string    `json:"author_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type JobPODAttachment struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	URL        string    `json:"url"`
	UploadedBy string    `json:"uploaded_by,omitempty"`
	UploadedAt time.Time `json:"uploaded_at"`
}

type JobTimelineEntry struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Summary    string            `json:"summary"`
	OccurredAt time.Time         `json:"occurred_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type Job struct {
	ID                      string             `json:"id"`
	Reference               string             `json:"reference,omitempty"`
	TenantID                string             `json:"tenant_id"`
	Pickup                  JobStop            `json:"pickup"`
	Dropoff                 JobStop            `json:"dropoff"`
	CargoType               string             `json:"cargo_type,omitempty"`
	CargoDescription        string             `json:"cargo_description,omitempty"`
	AmountNgn               float64            `json:"amount_ngn,omitempty"`
	Deadline                *time.Time         `json:"deadline,omitempty"`
	RequiredVehicleType     string             `json:"required_vehicle_type,omitempty"`
	LoadWeightKg            float64            `json:"load_weight_kg,omitempty"`
	LoadVolumeM3            float64            `json:"load_volume_m3,omitempty"`
	EstimatedServiceMinutes int                `json:"estimated_service_minutes,omitempty"`
	Priority                JobPriority        `json:"priority"`
	TimeWindow              JobTimeWindow      `json:"time_window"`
	Status                  JobStatus          `json:"status"`
	AssignedDriverID        string             `json:"assigned_driver_id,omitempty"`
	AssignedVehicleID       string             `json:"assigned_vehicle_id,omitempty"`
	CancellationReason      string             `json:"cancellation_reason,omitempty"`
	AcknowledgedAt          *time.Time         `json:"acknowledged_at,omitempty"`
	Notes                   []JobNote          `json:"notes"`
	PODAttachments          []JobPODAttachment `json:"pod_attachments"`
	Timeline                []JobTimelineEntry `json:"timeline"`
	CreatedAt               time.Time          `json:"created_at"`
	UpdatedAt               time.Time          `json:"updated_at"`
}

func NewJob(params Job) (Job, error) {
	if strings.TrimSpace(params.ID) == "" {
		return Job{}, errors.New("job id is required")
	}
	if strings.TrimSpace(params.TenantID) == "" {
		return Job{}, errors.New("tenant id is required")
	}
	if strings.TrimSpace(params.Pickup.Address) == "" {
		return Job{}, errors.New("pickup address is required")
	}
	if strings.TrimSpace(params.Dropoff.Address) == "" {
		return Job{}, errors.New("dropoff address is required")
	}
	if params.CreatedAt.IsZero() || params.UpdatedAt.IsZero() {
		return Job{}, errors.New("created_at and updated_at are required")
	}
	if params.LoadWeightKg < 0 {
		return Job{}, errors.New("load_weight_kg cannot be negative")
	}
	if params.AmountNgn < 0 {
		return Job{}, errors.New("amount_ngn cannot be negative")
	}
	if params.LoadVolumeM3 < 0 {
		return Job{}, errors.New("load_volume_m3 cannot be negative")
	}
	if params.EstimatedServiceMinutes < 0 {
		return Job{}, errors.New("estimated_service_minutes cannot be negative")
	}

	priority, err := NormalizePriority(string(params.Priority))
	if err != nil {
		return Job{}, err
	}

	status := params.Status
	if status == "" {
		status = StatusCreated
	}
	if !isKnownStatus(status) {
		return Job{}, fmt.Errorf("unknown job status %q", status)
	}

	if err := validateTimeWindow(params.TimeWindow); err != nil {
		return Job{}, err
	}

	job := Job{
		ID:                      strings.TrimSpace(params.ID),
		Reference:               strings.TrimSpace(params.Reference),
		TenantID:                strings.TrimSpace(params.TenantID),
		Pickup:                  normalizeStop(params.Pickup),
		Dropoff:                 normalizeStop(params.Dropoff),
		CargoType:               strings.TrimSpace(params.CargoType),
		CargoDescription:        strings.TrimSpace(params.CargoDescription),
		AmountNgn:               params.AmountNgn,
		Deadline:                normalizeOptionalTime(params.Deadline),
		RequiredVehicleType:     strings.TrimSpace(params.RequiredVehicleType),
		LoadWeightKg:            params.LoadWeightKg,
		LoadVolumeM3:            params.LoadVolumeM3,
		EstimatedServiceMinutes: params.EstimatedServiceMinutes,
		Priority:                priority,
		TimeWindow:              params.TimeWindow,
		Status:                  status,
		AssignedDriverID:        strings.TrimSpace(params.AssignedDriverID),
		AssignedVehicleID:       strings.TrimSpace(params.AssignedVehicleID),
		CancellationReason:      strings.TrimSpace(params.CancellationReason),
		AcknowledgedAt:          params.AcknowledgedAt,
		Notes:                   append([]JobNote{}, params.Notes...),
		PODAttachments:          append([]JobPODAttachment{}, params.PODAttachments...),
		Timeline:                append([]JobTimelineEntry{}, params.Timeline...),
		CreatedAt:               params.CreatedAt.UTC(),
		UpdatedAt:               params.UpdatedAt.UTC(),
	}

	return job, nil
}

func NormalizePriority(value string) (JobPriority, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "medium", string(PriorityNormal):
		return PriorityNormal, nil
	case string(PriorityLow):
		return PriorityLow, nil
	case string(PriorityHigh):
		return PriorityHigh, nil
	case "urgent", "critical":
		return PriorityUrgent, nil
	default:
		return "", fmt.Errorf("priority must be one of low, normal, high, urgent")
	}
}

func NormalizeStatus(value string) (JobStatus, error) {
	status := JobStatus(strings.ToLower(strings.TrimSpace(value)))
	if !isKnownStatus(status) {
		return "", fmt.Errorf("unsupported job status %q", value)
	}
	return status, nil
}

func (j Job) SetScheduling(priority JobPriority, timeWindow JobTimeWindow, now time.Time, timelineID string) (Job, error) {
	if j.Status == StatusCancelled || j.Status == StatusDelivered {
		return Job{}, errors.New("cannot update scheduling for completed/cancelled job")
	}
	if err := validateTimeWindow(timeWindow); err != nil {
		return Job{}, err
	}

	updated := j
	updated.Priority = priority
	updated.TimeWindow = timeWindow
	updated.UpdatedAt = now.UTC()
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "scheduling_updated",
		Summary:    "Priority/time window updated",
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"priority": string(priority),
		},
	})
	return updated, nil
}

func (j Job) Assign(driverID, vehicleID string, now time.Time, timelineID string) (Job, error) {
	driverID = strings.TrimSpace(driverID)
	vehicleID = strings.TrimSpace(vehicleID)
	if driverID == "" {
		return Job{}, errors.New("driver id is required")
	}
	if vehicleID == "" {
		return Job{}, errors.New("vehicle id is required")
	}
	if j.Status == StatusCancelled || j.Status == StatusDelivered {
		return Job{}, errors.New("cannot assign a completed/cancelled job")
	}

	updated := j
	previousDriver := updated.AssignedDriverID
	previousVehicle := updated.AssignedVehicleID
	updated.AssignedDriverID = driverID
	updated.AssignedVehicleID = vehicleID
	updated.Status = StatusAssigned
	updated.UpdatedAt = now.UTC()

	eventType := "job_assigned"
	summary := "Job assigned to driver/vehicle"
	if previousDriver != "" || previousVehicle != "" {
		eventType = "job_reassigned"
		summary = "Job reassigned to a new driver/vehicle"
	}

	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       eventType,
		Summary:    summary,
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"driver_id":           driverID,
			"vehicle_id":          vehicleID,
			"previous_driver_id":  previousDriver,
			"previous_vehicle_id": previousVehicle,
		},
	})
	return updated, nil
}

func (j Job) Cancel(reason string, now time.Time, timelineID string) (Job, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Job{}, errors.New("cancellation reason is required")
	}
	if j.Status == StatusCancelled {
		return Job{}, errors.New("job is already cancelled")
	}
	if j.Status == StatusDelivered {
		return Job{}, errors.New("cannot cancel a delivered job")
	}

	updated := j
	updated.Status = StatusCancelled
	updated.CancellationReason = reason
	updated.UpdatedAt = now.UTC()
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "job_cancelled",
		Summary:    "Job cancelled",
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"reason": reason,
		},
	})
	return updated, nil
}

func (j Job) Acknowledge(driverID string, now time.Time, timelineID string) (Job, error) {
	driverID = strings.TrimSpace(driverID)
	if j.Status == StatusCancelled {
		return Job{}, errors.New("cannot acknowledge a cancelled job")
	}
	if j.AssignedDriverID == "" {
		return Job{}, errors.New("job has no assigned driver")
	}
	if driverID != "" && driverID != j.AssignedDriverID {
		return Job{}, errors.New("acknowledgement must come from the assigned driver")
	}

	updated := j
	ackTime := now.UTC()
	updated.AcknowledgedAt = &ackTime
	updated.UpdatedAt = ackTime
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "job_acknowledged",
		Summary:    "Driver acknowledged job instruction",
		OccurredAt: ackTime,
		Metadata: map[string]string{
			"driver_id": updated.AssignedDriverID,
		},
	})
	return updated, nil
}

func (j Job) UpdateStatus(status JobStatus, now time.Time, timelineID string) (Job, error) {
	if !isKnownStatus(status) {
		return Job{}, fmt.Errorf("unsupported job status %q", status)
	}
	if status == StatusCancelled {
		return Job{}, errors.New("use cancel action to set cancelled status with reason")
	}
	if !isTransitionAllowed(j.Status, status) {
		return Job{}, fmt.Errorf("invalid status transition %q -> %q", j.Status, status)
	}

	updated := j
	previousStatus := updated.Status
	updated.Status = status
	updated.UpdatedAt = now.UTC()
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "job_status_updated",
		Summary:    "Driver updated job status",
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"from_status": string(previousStatus),
			"to_status":   string(status),
		},
	})
	return updated, nil
}

func (j Job) AddPODAttachment(attachment JobPODAttachment, now time.Time, timelineID string) (Job, error) {
	if j.Status != StatusDelivered {
		return Job{}, errors.New("proof-of-delivery attachments are allowed only after delivery")
	}
	if strings.TrimSpace(attachment.ID) == "" {
		return Job{}, errors.New("attachment id is required")
	}
	if strings.TrimSpace(attachment.Kind) == "" {
		return Job{}, errors.New("attachment kind is required")
	}
	if strings.TrimSpace(attachment.URL) == "" {
		return Job{}, errors.New("attachment url is required")
	}

	updated := j
	attachment.ID = strings.TrimSpace(attachment.ID)
	attachment.Kind = strings.TrimSpace(attachment.Kind)
	attachment.URL = strings.TrimSpace(attachment.URL)
	attachment.UploadedBy = strings.TrimSpace(attachment.UploadedBy)
	attachment.UploadedAt = now.UTC()
	updated.PODAttachments = append(updated.PODAttachments, attachment)
	updated.UpdatedAt = now.UTC()
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "pod_attachment_added",
		Summary:    "Proof-of-delivery attachment uploaded",
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"attachment_id":   attachment.ID,
			"attachment_kind": attachment.Kind,
		},
	})
	return updated, nil
}

func (j Job) AddNote(note JobNote, now time.Time, timelineID string) (Job, error) {
	if strings.TrimSpace(note.ID) == "" {
		return Job{}, errors.New("note id is required")
	}
	if strings.TrimSpace(note.Text) == "" {
		return Job{}, errors.New("note text is required")
	}

	updated := j
	note.ID = strings.TrimSpace(note.ID)
	note.Text = strings.TrimSpace(note.Text)
	note.AuthorID = strings.TrimSpace(note.AuthorID)
	note.CreatedAt = now.UTC()
	updated.Notes = append(updated.Notes, note)
	updated.UpdatedAt = now.UTC()
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "job_note_added",
		Summary:    "Dispatcher note added to job",
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"note_id": note.ID,
		},
	})
	return updated, nil
}

func (j Job) MarkDriverInstructionSent(channel string, now time.Time, timelineID string) Job {
	updated := j
	updated.UpdatedAt = now.UTC()
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "driver_instruction_sent",
		Summary:    "Driver instruction delivered",
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"channel":   strings.TrimSpace(channel),
			"driver_id": updated.AssignedDriverID,
		},
	})
	return updated
}

func (j *Job) appendTimeline(entry JobTimelineEntry) {
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("timeline-%d", time.Now().UnixNano())
	}
	j.Timeline = append(j.Timeline, entry)
}

func normalizeStop(stop JobStop) JobStop {
	return JobStop{
		Address:      strings.TrimSpace(stop.Address),
		ContactName:  strings.TrimSpace(stop.ContactName),
		ContactPhone: strings.TrimSpace(stop.ContactPhone),
		Latitude:     stop.Latitude,
		Longitude:    stop.Longitude,
		Notes:        strings.TrimSpace(stop.Notes),
	}
}

func normalizeOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

func validateTimeWindow(window JobTimeWindow) error {
	if window.PickupStart != nil && window.PickupEnd != nil && window.PickupStart.After(*window.PickupEnd) {
		return errors.New("pickup_start must be before pickup_end")
	}
	if window.DropoffStart != nil && window.DropoffEnd != nil && window.DropoffStart.After(*window.DropoffEnd) {
		return errors.New("dropoff_start must be before dropoff_end")
	}
	return nil
}

func isKnownStatus(status JobStatus) bool {
	switch status {
	case StatusCreated, StatusAssigned, StatusEnRoute, StatusArrived, StatusLoaded, StatusDelivered, StatusCancelled:
		return true
	default:
		return false
	}
}

func isTransitionAllowed(from, to JobStatus) bool {
	if from == to {
		return true
	}

	switch from {
	case StatusCreated:
		return to == StatusAssigned || to == StatusCancelled
	case StatusAssigned:
		return to == StatusEnRoute || to == StatusCancelled
	case StatusEnRoute:
		return to == StatusArrived || to == StatusCancelled
	case StatusArrived:
		return to == StatusLoaded || to == StatusCancelled
	case StatusLoaded:
		return to == StatusDelivered || to == StatusCancelled
	default:
		return false
	}
}
