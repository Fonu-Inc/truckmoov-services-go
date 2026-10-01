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

// JobPunctuality records how a delivered job met its deadline. Stored at
// completion rather than recomputed later: the traffic that made a driver late
// is not observable after the fact.
type JobPunctuality struct {
	Deadline                time.Time `json:"deadline"`
	CompletedAt             time.Time `json:"completed_at"`
	TrafficAllowanceSeconds int64     `json:"traffic_allowance_seconds"`
	LatenessSeconds         int64     `json:"lateness_seconds"`
	OnTime                  bool      `json:"on_time"`
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

type DriverStatusRequestState string

const (
	DriverStatusRequestPending   DriverStatusRequestState = "pending"
	DriverStatusRequestConfirmed DriverStatusRequestState = "confirmed"
)

type DriverStatusRequest struct {
	ID              string                   `json:"id"`
	RequestedStatus JobStatus                `json:"requested_status"`
	Keyword         string                   `json:"keyword"`
	Channel         string                   `json:"channel"`
	DriverID        string                   `json:"driver_id"`
	DriverPhone     string                   `json:"driver_phone,omitempty"`
	RawMessage      string                   `json:"raw_message,omitempty"`
	SourceMessageID string                   `json:"source_message_id,omitempty"`
	State           DriverStatusRequestState `json:"state"`
	RequestedAt     time.Time                `json:"requested_at"`
	ConfirmedAt     *time.Time               `json:"confirmed_at,omitempty"`
	ConfirmedBy     string                   `json:"confirmed_by,omitempty"`
}

type Job struct {
	ID                      string                `json:"id"`
	Reference               string                `json:"reference,omitempty"`
	TenantID                string                `json:"tenant_id"`
	Pickup                  JobStop               `json:"pickup"`
	Dropoff                 JobStop               `json:"dropoff"`
	Stops                   []JobStop             `json:"stops,omitempty"`
	CargoType               string                `json:"cargo_type,omitempty"`
	CargoDescription        string                `json:"cargo_description,omitempty"`
	AmountNgn               float64               `json:"amount_ngn,omitempty"`
	Deadline                *time.Time            `json:"deadline,omitempty"`
	RequiredVehicleType     string                `json:"required_vehicle_type,omitempty"`
	LoadWeightKg            float64               `json:"load_weight_kg,omitempty"`
	LoadVolumeM3            float64               `json:"load_volume_m3,omitempty"`
	EstimatedServiceMinutes int                   `json:"estimated_service_minutes,omitempty"`
	Priority                JobPriority           `json:"priority"`
	TimeWindow              JobTimeWindow         `json:"time_window"`
	Status                  JobStatus             `json:"status"`
	AssignedDriverID        string                `json:"assigned_driver_id,omitempty"`
	AssignedDriverPhone     string                `json:"assigned_driver_phone,omitempty"`
	AssignedVehicleID       string                `json:"assigned_vehicle_id,omitempty"`
	CancellationReason      string                `json:"cancellation_reason,omitempty"`
	AcknowledgedAt          *time.Time            `json:"acknowledged_at,omitempty"`
	Notes                   []JobNote             `json:"notes"`
	PODAttachments          []JobPODAttachment    `json:"pod_attachments"`
	DriverStatusRequests    []DriverStatusRequest `json:"driver_status_requests"`
	Timeline                []JobTimelineEntry    `json:"timeline"`
	Punctuality             *JobPunctuality       `json:"punctuality,omitempty"`
	CreatedAt               time.Time             `json:"created_at"`
	UpdatedAt               time.Time             `json:"updated_at"`
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
		Stops:                   normalizeStops(params.Stops),
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
		AssignedDriverPhone:     strings.TrimSpace(params.AssignedDriverPhone),
		AssignedVehicleID:       strings.TrimSpace(params.AssignedVehicleID),
		CancellationReason:      strings.TrimSpace(params.CancellationReason),
		AcknowledgedAt:          params.AcknowledgedAt,
		Notes:                   append([]JobNote{}, params.Notes...),
		PODAttachments:          append([]JobPODAttachment{}, params.PODAttachments...),
		DriverStatusRequests:    append([]DriverStatusRequest{}, params.DriverStatusRequests...),
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

type JobDetailsUpdate struct {
	Pickup           JobStop
	Dropoff          JobStop
	Stops            []JobStop
	CargoType        string
	CargoDescription string
	AmountNgn        float64
	Deadline         *time.Time
	LoadWeightKg     float64
	LoadVolumeM3     float64
}

// JobDetailsPatch carries only the fields a caller sent. Nil keeps the job's
// current value.
type JobDetailsPatch struct {
	Pickup           *JobStop
	Dropoff          *JobStop
	Stops            *[]JobStop
	CargoType        *string
	CargoDescription *string
	AmountNgn        *float64
	Deadline         *time.Time
	ClearDeadline    bool
	LoadWeightKg     *float64
	LoadVolumeM3     *float64
}

func (j Job) PatchDetails(patch JobDetailsPatch, now time.Time, timelineID string) (Job, error) {
	return j.UpdateDetails(patch.over(j.details()), now, timelineID)
}

func (j Job) details() JobDetailsUpdate {
	return JobDetailsUpdate{
		Pickup:           j.Pickup,
		Dropoff:          j.Dropoff,
		Stops:            j.Stops,
		CargoType:        j.CargoType,
		CargoDescription: j.CargoDescription,
		AmountNgn:        j.AmountNgn,
		Deadline:         j.Deadline,
		LoadWeightKg:     j.LoadWeightKg,
		LoadVolumeM3:     j.LoadVolumeM3,
	}
}

func (p JobDetailsPatch) over(base JobDetailsUpdate) JobDetailsUpdate {
	merged := base
	setIfSent(&merged.Pickup, p.Pickup)
	setIfSent(&merged.Dropoff, p.Dropoff)
	setIfSent(&merged.Stops, p.Stops)
	setIfSent(&merged.CargoType, p.CargoType)
	setIfSent(&merged.CargoDescription, p.CargoDescription)
	setIfSent(&merged.AmountNgn, p.AmountNgn)
	setIfSent(&merged.LoadWeightKg, p.LoadWeightKg)
	setIfSent(&merged.LoadVolumeM3, p.LoadVolumeM3)
	if p.Deadline != nil {
		merged.Deadline = p.Deadline
	}
	if p.ClearDeadline {
		merged.Deadline = nil
	}
	return merged
}

func setIfSent[T any](target *T, sent *T) {
	if sent != nil {
		*target = *sent
	}
}

func (j Job) UpdateDetails(update JobDetailsUpdate, now time.Time, timelineID string) (Job, error) {
	if j.Status == StatusCancelled || j.Status == StatusDelivered {
		return Job{}, errors.New("cannot edit a completed/cancelled job")
	}
	if strings.TrimSpace(update.Pickup.Address) == "" {
		return Job{}, errors.New("pickup address is required")
	}
	if strings.TrimSpace(update.Dropoff.Address) == "" {
		return Job{}, errors.New("dropoff address is required")
	}
	if update.AmountNgn < 0 {
		return Job{}, errors.New("amount_ngn cannot be negative")
	}
	if update.LoadWeightKg < 0 {
		return Job{}, errors.New("load_weight_kg cannot be negative")
	}
	if update.LoadVolumeM3 < 0 {
		return Job{}, errors.New("load_volume_m3 cannot be negative")
	}

	updated := j
	updated.Pickup = normalizeStop(update.Pickup)
	updated.Dropoff = normalizeStop(update.Dropoff)
	updated.Stops = normalizeStops(update.Stops)
	updated.CargoType = strings.TrimSpace(update.CargoType)
	updated.CargoDescription = strings.TrimSpace(update.CargoDescription)
	updated.AmountNgn = update.AmountNgn
	updated.Deadline = normalizeOptionalTime(update.Deadline)
	updated.LoadWeightKg = update.LoadWeightKg
	updated.LoadVolumeM3 = update.LoadVolumeM3
	updated.UpdatedAt = now.UTC()
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "job_details_updated",
		Summary:    "Job details updated",
		OccurredAt: now.UTC(),
	})
	return updated, nil
}

func (j Job) Assign(driverID, driverPhone, vehicleID string, now time.Time, timelineID string) (Job, error) {
	driverID = strings.TrimSpace(driverID)
	driverPhone = strings.TrimSpace(driverPhone)
	vehicleID = strings.TrimSpace(vehicleID)
	if driverID == "" && vehicleID == "" {
		return Job{}, errors.New("driver id or vehicle id is required")
	}
	if j.Status == StatusCancelled || j.Status == StatusDelivered {
		return Job{}, errors.New("cannot assign a completed/cancelled job")
	}

	updated := j
	updated.applyAssignment(driverID, driverPhone, vehicleID)
	// Assigned means a driver has been told. A vehicle alone, or the same
	// driver again, leaves the status where it was.
	if updated.DriverChangedFrom(j) {
		updated.Status = StatusAssigned
	}
	updated.UpdatedAt = now.UTC()

	eventType, summary := assignmentEvent(j, updated)
	if eventType == "" {
		return updated, nil
	}
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       eventType,
		Summary:    summary,
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"driver_id":           updated.AssignedDriverID,
			"driver_phone":        updated.AssignedDriverPhone,
			"vehicle_id":          updated.AssignedVehicleID,
			"previous_driver_id":  j.AssignedDriverID,
			"previous_vehicle_id": j.AssignedVehicleID,
		},
	})
	return updated, nil
}

// DriverChangedFrom reports whether the job now has a different driver than
// before: newly assigned or replaced.
func (j Job) DriverChangedFrom(before Job) bool {
	return j.AssignedDriverID != before.AssignedDriverID
}

func (j *Job) applyAssignment(driverID, driverPhone, vehicleID string) {
	if driverID != "" {
		// A new driver never inherits the previous driver's phone.
		if driverPhone != "" || driverID != j.AssignedDriverID {
			j.AssignedDriverPhone = driverPhone
		}
		j.AssignedDriverID = driverID
	}
	if vehicleID != "" {
		j.AssignedVehicleID = vehicleID
	}
}

// assignmentEvent names what an assignment changed. A driver change wins over
// a vehicle change; nothing changed means no timeline entry.
func assignmentEvent(before, after Job) (string, string) {
	vehicleChanged := after.AssignedVehicleID != before.AssignedVehicleID
	switch {
	case after.DriverChangedFrom(before) && before.AssignedDriverID == "":
		return "job_assigned", "Job assigned to driver/vehicle"
	case after.DriverChangedFrom(before):
		return "job_reassigned", "Job reassigned to a new driver/vehicle"
	case vehicleChanged && before.AssignedVehicleID == "":
		return "vehicle_assigned", "Vehicle assigned"
	case vehicleChanged:
		return "vehicle_assigned", "Vehicle changed"
	default:
		return "", ""
	}
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
		Summary:    "Job status updated",
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"from_status": string(previousStatus),
			"to_status":   string(status),
		},
	})
	return updated, nil
}

func (j Job) ReportDriverStatusRequest(requestedStatus JobStatus, driverID, driverPhone, keyword, rawMessage, sourceMessageID, channel string, now time.Time, requestID, timelineID string) (Job, DriverStatusRequest, error) {
	if strings.TrimSpace(driverID) == "" {
		return Job{}, DriverStatusRequest{}, errors.New("driver id is required")
	}
	if strings.TrimSpace(keyword) == "" {
		return Job{}, DriverStatusRequest{}, errors.New("driver keyword is required")
	}
	if strings.TrimSpace(channel) == "" {
		return Job{}, DriverStatusRequest{}, errors.New("channel is required")
	}
	if strings.TrimSpace(requestID) == "" {
		return Job{}, DriverStatusRequest{}, errors.New("driver status request id is required")
	}
	if j.Status == StatusCancelled || j.Status == StatusDelivered {
		return Job{}, DriverStatusRequest{}, errors.New("driver status reports are not allowed for the current job status")
	}
	if j.AssignedDriverID == "" {
		return Job{}, DriverStatusRequest{}, errors.New("job has no assigned driver")
	}
	if strings.TrimSpace(driverID) != strings.TrimSpace(j.AssignedDriverID) {
		return Job{}, DriverStatusRequest{}, errors.New("driver status report must come from the assigned driver")
	}
	if strings.TrimSpace(j.AssignedDriverPhone) != "" && strings.TrimSpace(driverPhone) != "" && normalizeDispatchPhone(driverPhone) != normalizeDispatchPhone(j.AssignedDriverPhone) {
		return Job{}, DriverStatusRequest{}, errors.New("driver status report must come from the assigned driver's stored phone number")
	}
	if !isDriverReportedStatus(requestedStatus) {
		return Job{}, DriverStatusRequest{}, fmt.Errorf("unsupported driver reported status %q", requestedStatus)
	}

	expectedStatus, ok := nextDriverReportedStatus(j.Status)
	if !ok {
		return Job{}, DriverStatusRequest{}, errors.New("driver status reports are not allowed for the current job status")
	}
	if requestedStatus != expectedStatus {
		return Job{}, DriverStatusRequest{}, fmt.Errorf("driver can only report next status %q", expectedStatus)
	}

	for _, existing := range j.DriverStatusRequests {
		if existing.State != DriverStatusRequestPending {
			continue
		}
		if strings.TrimSpace(sourceMessageID) != "" && strings.TrimSpace(existing.SourceMessageID) == strings.TrimSpace(sourceMessageID) {
			return Job{}, DriverStatusRequest{}, errors.New("driver status report for this message has already been recorded")
		}
		if existing.RequestedStatus == requestedStatus {
			return Job{}, DriverStatusRequest{}, errors.New("a pending driver status request already exists for this status")
		}
	}

	requestedAt := now.UTC()
	request := DriverStatusRequest{
		ID:              strings.TrimSpace(requestID),
		RequestedStatus: requestedStatus,
		Keyword:         strings.TrimSpace(keyword),
		Channel:         strings.TrimSpace(channel),
		DriverID:        strings.TrimSpace(driverID),
		DriverPhone:     strings.TrimSpace(driverPhone),
		RawMessage:      strings.TrimSpace(rawMessage),
		SourceMessageID: strings.TrimSpace(sourceMessageID),
		State:           DriverStatusRequestPending,
		RequestedAt:     requestedAt,
	}

	updated := j
	updated.DriverStatusRequests = append(updated.DriverStatusRequests, request)
	updated.UpdatedAt = requestedAt
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "driver_status_reported",
		Summary:    fmt.Sprintf("Driver reported %s via %s", requestedStatus, strings.TrimSpace(channel)),
		OccurredAt: requestedAt,
		Metadata: map[string]string{
			"request_id":        request.ID,
			"requested_status":  string(request.RequestedStatus),
			"driver_id":         request.DriverID,
			"driver_phone":      request.DriverPhone,
			"keyword":           request.Keyword,
			"channel":           request.Channel,
			"source_message_id": request.SourceMessageID,
		},
	})

	return updated, request, nil
}

func (j Job) ConfirmDriverStatusRequest(requestID, confirmedBy string, now time.Time, confirmationTimelineID, statusTimelineID string) (Job, DriverStatusRequest, error) {
	if strings.TrimSpace(requestID) == "" {
		return Job{}, DriverStatusRequest{}, errors.New("driver status request id is required")
	}

	requestIndex := -1
	for idx, request := range j.DriverStatusRequests {
		if strings.TrimSpace(request.ID) == strings.TrimSpace(requestID) {
			requestIndex = idx
			break
		}
	}
	if requestIndex == -1 {
		return Job{}, DriverStatusRequest{}, errors.New("driver status request not found")
	}
	if j.DriverStatusRequests[requestIndex].State != DriverStatusRequestPending {
		return Job{}, DriverStatusRequest{}, errors.New("driver status request is not pending")
	}

	request := j.DriverStatusRequests[requestIndex]
	if !isTransitionAllowed(j.Status, request.RequestedStatus) {
		return Job{}, DriverStatusRequest{}, fmt.Errorf("invalid status transition %q -> %q", j.Status, request.RequestedStatus)
	}

	confirmedAt := now.UTC()
	updated := j
	updated.DriverStatusRequests[requestIndex].State = DriverStatusRequestConfirmed
	updated.DriverStatusRequests[requestIndex].ConfirmedBy = strings.TrimSpace(confirmedBy)
	updated.DriverStatusRequests[requestIndex].ConfirmedAt = &confirmedAt
	request = updated.DriverStatusRequests[requestIndex]

	previousStatus := updated.Status
	updated.Status = request.RequestedStatus
	updated.UpdatedAt = confirmedAt
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(confirmationTimelineID),
		Type:       "driver_status_confirmed",
		Summary:    "Dispatcher confirmed driver reported status",
		OccurredAt: confirmedAt,
		Metadata: map[string]string{
			"request_id":       request.ID,
			"requested_status": string(request.RequestedStatus),
			"driver_id":        request.DriverID,
			"confirmed_by":     strings.TrimSpace(confirmedBy),
		},
	})
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(statusTimelineID),
		Type:       "job_status_updated",
		Summary:    "Job status updated",
		OccurredAt: confirmedAt,
		Metadata: map[string]string{
			"from_status":  string(previousStatus),
			"to_status":    string(request.RequestedStatus),
			"request_id":   request.ID,
			"source":       "driver_status_confirmation",
			"confirmed_by": strings.TrimSpace(confirmedBy),
		},
	})

	return updated, request, nil
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

// TransferToCarrier hands operational ownership of the job to another tenant.
//
// This is what "accepting an offer assigns the job" means concretely: the job
// becomes the carrier's to dispatch. It does not pick a driver or vehicle —
// the carrier has neither yet, and choosing one is their own dispatcher's job
// afterwards through the ordinary Assign flow. Conflating the two would need a
// driver selected before the carrier has even seen the job.
func (j Job) TransferToCarrier(carrierTenantID string, now time.Time, timelineID string) (Job, error) {
	carrierTenantID = strings.TrimSpace(carrierTenantID)
	if carrierTenantID == "" {
		return Job{}, errors.New("carrier tenant id is required")
	}
	if j.Status == StatusCancelled || j.Status == StatusDelivered {
		return Job{}, errors.New("cannot transfer a completed/cancelled job")
	}
	if j.TenantID == carrierTenantID {
		return Job{}, errors.New("job already belongs to this tenant")
	}

	updated := j
	previousTenant := updated.TenantID
	updated.TenantID = carrierTenantID
	updated.UpdatedAt = now.UTC()
	updated.appendTimeline(JobTimelineEntry{
		ID:         strings.TrimSpace(timelineID),
		Type:       "job_offer_accepted",
		Summary:    "Job accepted via offer and handed to the carrier",
		OccurredAt: now.UTC(),
		Metadata: map[string]string{
			"previous_tenant_id": previousTenant,
			"carrier_tenant_id":  carrierTenantID,
		},
	})
	return updated, nil
}

// IsActive reports whether the job still needs work done on it. Delivered and
// cancelled jobs are the only terminal states; everything else counts against
// a carrier's active-job cap.
func (j Job) IsActive() bool {
	return j.Status != StatusDelivered && j.Status != StatusCancelled
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

func normalizeStops(stops []JobStop) []JobStop {
	if len(stops) == 0 {
		return nil
	}
	normalized := make([]JobStop, 0, len(stops))
	for _, stop := range stops {
		if strings.TrimSpace(stop.Address) == "" && stop.Latitude == nil && stop.Longitude == nil {
			continue
		}
		normalized = append(normalized, normalizeStop(stop))
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
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

func isDriverReportedStatus(status JobStatus) bool {
	switch status {
	case StatusEnRoute, StatusArrived, StatusLoaded, StatusDelivered:
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

func nextDriverReportedStatus(current JobStatus) (JobStatus, bool) {
	switch current {
	case StatusAssigned:
		return StatusEnRoute, true
	case StatusEnRoute:
		return StatusArrived, true
	case StatusArrived:
		return StatusLoaded, true
	case StatusLoaded:
		return StatusDelivered, true
	default:
		return "", false
	}
}

func normalizeDispatchPhone(value string) string {
	normalized := strings.TrimSpace(value)
	normalized = strings.TrimPrefix(normalized, "whatsapp:")
	replacer := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	normalized = replacer.Replace(normalized)
	return strings.TrimPrefix(normalized, "+")
}
