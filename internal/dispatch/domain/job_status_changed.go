package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type JobStatusChanged struct {
	EventID           string
	OccurredAt        time.Time
	TenantID          string
	JobID             string
	PreviousStatus    string
	NewStatus         string
	AssignedDriverID  string
	AssignedVehicleID string
	Reason            string
}

func NewJobStatusChanged(params JobStatusChanged) (JobStatusChanged, error) {
	if strings.TrimSpace(params.EventID) == "" {
		return JobStatusChanged{}, errors.New("event id is required")
	}
	if strings.TrimSpace(params.TenantID) == "" {
		return JobStatusChanged{}, errors.New("tenant id is required")
	}
	if strings.TrimSpace(params.JobID) == "" {
		return JobStatusChanged{}, errors.New("job id is required")
	}
	if strings.TrimSpace(params.NewStatus) == "" {
		return JobStatusChanged{}, errors.New("new status is required")
	}
	if params.OccurredAt.IsZero() {
		return JobStatusChanged{}, errors.New("occurred_at is required")
	}

	return JobStatusChanged{
		EventID:           strings.TrimSpace(params.EventID),
		OccurredAt:        params.OccurredAt.UTC(),
		TenantID:          strings.TrimSpace(params.TenantID),
		JobID:             strings.TrimSpace(params.JobID),
		PreviousStatus:    strings.TrimSpace(params.PreviousStatus),
		NewStatus:         strings.TrimSpace(params.NewStatus),
		AssignedDriverID:  strings.TrimSpace(params.AssignedDriverID),
		AssignedVehicleID: strings.TrimSpace(params.AssignedVehicleID),
		Reason:            strings.TrimSpace(params.Reason),
	}, nil
}

func (e JobStatusChanged) Subject() string {
	return fmt.Sprintf("tm.%s.job.status.changed", e.TenantID)
}
