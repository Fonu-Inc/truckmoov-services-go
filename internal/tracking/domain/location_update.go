package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type LocationUpdate struct {
	EventID    string
	OccurredAt time.Time
	TenantID   string
	VehicleID  string
	DeviceID   string
	Latitude   float64
	Longitude  float64
	SpeedKPH   *float64
	Heading    *float64
	AccuracyM  *float64
	FixTimeRFC string
}

func NewLocationUpdate(params LocationUpdate) (LocationUpdate, error) {
	if strings.TrimSpace(params.EventID) == "" {
		return LocationUpdate{}, errors.New("event id is required")
	}
	if strings.TrimSpace(params.TenantID) == "" {
		return LocationUpdate{}, errors.New("tenant id is required")
	}
	if strings.TrimSpace(params.VehicleID) == "" {
		return LocationUpdate{}, errors.New("vehicle id is required")
	}
	if strings.TrimSpace(params.DeviceID) == "" {
		return LocationUpdate{}, errors.New("device id is required")
	}
	if params.OccurredAt.IsZero() {
		return LocationUpdate{}, errors.New("occurred_at is required")
	}
	if strings.TrimSpace(params.FixTimeRFC) == "" {
		return LocationUpdate{}, errors.New("fix_time is required")
	}

	return LocationUpdate{
		EventID:    strings.TrimSpace(params.EventID),
		OccurredAt: params.OccurredAt.UTC(),
		TenantID:   strings.TrimSpace(params.TenantID),
		VehicleID:  strings.TrimSpace(params.VehicleID),
		DeviceID:   strings.TrimSpace(params.DeviceID),
		Latitude:   params.Latitude,
		Longitude:  params.Longitude,
		SpeedKPH:   params.SpeedKPH,
		Heading:    params.Heading,
		AccuracyM:  params.AccuracyM,
		FixTimeRFC: strings.TrimSpace(params.FixTimeRFC),
	}, nil
}

func (e LocationUpdate) Subject() string {
	return fmt.Sprintf("tm.%s.vehicle.location.updated", e.TenantID)
}
