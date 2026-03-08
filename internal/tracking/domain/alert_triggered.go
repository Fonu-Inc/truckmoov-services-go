package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type AlertTriggered struct {
	EventID    string
	OccurredAt time.Time
	TenantID   string
	AlertID    string
	AlertType  string
	Severity   string
	VehicleID  string
	JobID      string
	Details    any
}

func NewAlertTriggered(params AlertTriggered) (AlertTriggered, error) {
	if strings.TrimSpace(params.EventID) == "" {
		return AlertTriggered{}, errors.New("event id is required")
	}
	if strings.TrimSpace(params.AlertID) == "" {
		return AlertTriggered{}, errors.New("alert id is required")
	}
	if strings.TrimSpace(params.TenantID) == "" {
		return AlertTriggered{}, errors.New("tenant id is required")
	}
	if strings.TrimSpace(params.AlertType) == "" {
		return AlertTriggered{}, errors.New("alert type is required")
	}
	if strings.TrimSpace(params.Severity) == "" {
		return AlertTriggered{}, errors.New("severity is required")
	}
	if params.OccurredAt.IsZero() {
		return AlertTriggered{}, errors.New("occurred_at is required")
	}

	return AlertTriggered{
		EventID:    strings.TrimSpace(params.EventID),
		OccurredAt: params.OccurredAt.UTC(),
		TenantID:   strings.TrimSpace(params.TenantID),
		AlertID:    strings.TrimSpace(params.AlertID),
		AlertType:  strings.TrimSpace(params.AlertType),
		Severity:   strings.TrimSpace(params.Severity),
		VehicleID:  strings.TrimSpace(params.VehicleID),
		JobID:      strings.TrimSpace(params.JobID),
		Details:    params.Details,
	}, nil
}

func (e AlertTriggered) Subject() string {
	return fmt.Sprintf("tm.%s.alert.triggered", e.TenantID)
}
