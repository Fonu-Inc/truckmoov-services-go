package application

import (
	"context"
	"time"
)

const telemetryWindow = 24 * time.Hour

// DeviceOverview backs the device-detail Overview tab.
type DeviceOverview struct {
	DeviceID        string   `json:"device_id"`
	VehicleID       string   `json:"vehicle_id,omitempty"`
	IMEI            string   `json:"imei,omitempty"`
	Health          string   `json:"health"`
	LastSeen        string   `json:"last_seen,omitempty"`
	SignalStrength  *float64 `json:"signal_strength,omitempty"`
	BatteryLevel    *float64 `json:"battery_level,omitempty"`
	Latitude        *float64 `json:"latitude,omitempty"`
	Longitude       *float64 `json:"longitude,omitempty"`
	FirmwareVersion string   `json:"firmware_version,omitempty"`
}

// DeviceConnectivity backs the Connectivity tab.
type DeviceConnectivity struct {
	DeviceID                 string   `json:"device_id"`
	Health                   string   `json:"health"`
	LastContact              string   `json:"last_contact,omitempty"`
	SecondsSinceContact      *float64 `json:"seconds_since_contact,omitempty"`
	StatusTimeoutSeconds     int      `json:"status_timeout_seconds"`
	ReportingIntervalSeconds *float64 `json:"reporting_interval_seconds,omitempty"`
	SampleCount24h           int      `json:"sample_count_24h"`
}

// DeviceTelemetry backs the Telemetry tab.
type DeviceTelemetry struct {
	DeviceID   string              `json:"device_id"`
	Latest     *TelemetrySample    `json:"latest,omitempty"`
	History    []TelemetrySample   `json:"history"`
	Aggregates TelemetryAggregates `json:"aggregates_24h"`
}

// DeviceFirmware backs the Firmware tab.
type DeviceFirmware struct {
	DeviceID                 string           `json:"device_id"`
	CurrentVersion           string           `json:"current_version,omitempty"`
	History                  []FirmwareReport `json:"history"`
	ReportingIntervalSeconds *float64         `json:"reporting_interval_seconds,omitempty"`
}

type DeviceTelemetryQueryUseCase struct {
	store                TelemetryStore
	clock                Clock
	statusTimeoutSeconds int
}

func NewDeviceTelemetryQueryUseCase(store TelemetryStore, clock Clock, statusTimeoutSeconds int) *DeviceTelemetryQueryUseCase {
	return &DeviceTelemetryQueryUseCase{store: store, clock: clock, statusTimeoutSeconds: statusTimeoutSeconds}
}

func (u *DeviceTelemetryQueryUseCase) Overview(ctx context.Context, tenantID, deviceID string) (DeviceOverview, error) {
	view := DeviceOverview{DeviceID: deviceID, Health: string(DeviceHealthUnknown)}
	if u.store == nil {
		return view, nil
	}
	latest, ok, err := u.store.LatestSample(ctx, tenantID, deviceID)
	if err != nil {
		return DeviceOverview{}, err
	}
	if !ok {
		return view, nil
	}
	view.VehicleID = latest.VehicleID
	view.IMEI = latest.IMEI
	view.LastSeen = latest.ObservedAt.UTC().Format(time.RFC3339)
	view.SignalStrength = latest.SignalStrength
	view.BatteryLevel = latest.BatteryLevel
	view.FirmwareVersion = latest.FirmwareVersion
	lat, lng := latest.Latitude, latest.Longitude
	view.Latitude = &lat
	view.Longitude = &lng
	view.Health = string(DeriveHealth(latest.ObservedAt, u.clock.Now(), u.statusTimeoutSeconds))
	return view, nil
}

func (u *DeviceTelemetryQueryUseCase) Connectivity(ctx context.Context, tenantID, deviceID string) (DeviceConnectivity, error) {
	view := DeviceConnectivity{
		DeviceID:             deviceID,
		Health:               string(DeviceHealthUnknown),
		StatusTimeoutSeconds: u.statusTimeoutSeconds,
	}
	if u.store == nil {
		return view, nil
	}
	now := u.clock.Now()
	latest, ok, err := u.store.LatestSample(ctx, tenantID, deviceID)
	if err != nil {
		return DeviceConnectivity{}, err
	}
	if ok {
		view.LastContact = latest.ObservedAt.UTC().Format(time.RFC3339)
		secs := now.Sub(latest.ObservedAt).Seconds()
		view.SecondsSinceContact = &secs
		view.Health = string(DeriveHealth(latest.ObservedAt, now, u.statusTimeoutSeconds))
	}

	agg, err := u.store.Aggregates(ctx, tenantID, deviceID, now.Add(-telemetryWindow), now)
	if err != nil {
		return DeviceConnectivity{}, err
	}
	view.SampleCount24h = agg.SampleCount
	view.ReportingIntervalSeconds = agg.AvgIntervalSeconds
	return view, nil
}

func (u *DeviceTelemetryQueryUseCase) Telemetry(ctx context.Context, tenantID, deviceID string, limit int) (DeviceTelemetry, error) {
	view := DeviceTelemetry{DeviceID: deviceID, History: []TelemetrySample{}}
	if u.store == nil {
		return view, nil
	}
	now := u.clock.Now()
	since := now.Add(-telemetryWindow)

	latest, ok, err := u.store.LatestSample(ctx, tenantID, deviceID)
	if err != nil {
		return DeviceTelemetry{}, err
	}
	if ok {
		view.Latest = latest
	}

	history, err := u.store.SampleHistory(ctx, tenantID, deviceID, since, limit)
	if err != nil {
		return DeviceTelemetry{}, err
	}
	view.History = history

	agg, err := u.store.Aggregates(ctx, tenantID, deviceID, since, now)
	if err != nil {
		return DeviceTelemetry{}, err
	}
	applyDataQuality(&agg, u.statusTimeoutSeconds)
	view.Aggregates = agg
	return view, nil
}

func (u *DeviceTelemetryQueryUseCase) Firmware(ctx context.Context, tenantID, deviceID string) (DeviceFirmware, error) {
	view := DeviceFirmware{DeviceID: deviceID, History: []FirmwareReport{}}
	if u.store == nil {
		return view, nil
	}
	now := u.clock.Now()

	latest, ok, err := u.store.LatestSample(ctx, tenantID, deviceID)
	if err != nil {
		return DeviceFirmware{}, err
	}
	if ok {
		view.CurrentVersion = latest.FirmwareVersion
	}

	history, err := u.store.FirmwareHistory(ctx, tenantID, deviceID)
	if err != nil {
		return DeviceFirmware{}, err
	}
	view.History = history

	agg, err := u.store.Aggregates(ctx, tenantID, deviceID, now.Add(-telemetryWindow), now)
	if err != nil {
		return DeviceFirmware{}, err
	}
	view.ReportingIntervalSeconds = agg.AvgIntervalSeconds
	return view, nil
}

// applyDataQuality estimates connectivity quality as received-vs-expected heartbeats
// over the window, where one heartbeat is expected per status-timeout interval. A
// silent device (zero samples) correctly yields 0%, the most diagnostic moment.
func applyDataQuality(agg *TelemetryAggregates, statusTimeoutSeconds int) {
	if statusTimeoutSeconds <= 0 {
		return
	}
	expected := telemetryWindow.Seconds() / float64(statusTimeoutSeconds)
	pct := float64(agg.SampleCount) / expected * 100
	if pct > 100 {
		pct = 100
	}
	agg.DataQualityPct = &pct
}
