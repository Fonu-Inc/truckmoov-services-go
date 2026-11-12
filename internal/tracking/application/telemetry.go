package application

import (
	"context"
	"time"
)

// TelemetrySample is one decoded device report persisted to the time-series store.
// GPS is always present; the remaining signals are optional (nil when the vendor
// payload omits them).
type TelemetrySample struct {
	TenantID        string     `json:"tenant_id"`
	DeviceID        string     `json:"device_id"`
	VehicleID       string     `json:"vehicle_id"`
	IMEI            string     `json:"imei"`
	Latitude        float64    `json:"latitude"`
	Longitude       float64    `json:"longitude"`
	SpeedKPH        *float64   `json:"speed_kph,omitempty"`
	Heading         *float64   `json:"heading,omitempty"`
	AccuracyM       *float64   `json:"accuracy_m,omitempty"`
	BatteryLevel    *float64   `json:"battery_level,omitempty"`
	SignalStrength  *float64   `json:"signal_strength,omitempty"`
	FuelLevel       *float64   `json:"fuel_level,omitempty"`
	EngineOn        *bool      `json:"engine_on,omitempty"`
	FirmwareVersion string     `json:"firmware_version,omitempty"`
	BatteryVoltage  *float64   `json:"battery_voltage,omitempty"`
	PowerVoltage    *float64   `json:"power_voltage,omitempty"`
	OdometerM       *float64   `json:"odometer_m,omitempty"`
	ICCID           string     `json:"iccid,omitempty"`
	Alarm           string     `json:"alarm,omitempty"`
	NoFix           bool       `json:"no_fix,omitempty"`
	Protocol        string     `json:"protocol,omitempty"`
	FixTime         *time.Time `json:"fix_time,omitempty"`
	ObservedAt      time.Time  `json:"observed_at"`
	EventID         string     `json:"event_id,omitempty"`
}

// TelemetryAggregates is the 24h rolling data-quality summary for a device.
type TelemetryAggregates struct {
	SampleCount        int      `json:"sample_count"`
	AvgIntervalSeconds *float64 `json:"avg_interval_seconds,omitempty"`
	DataQualityPct     *float64 `json:"data_quality_pct,omitempty"`
	WindowStart        string   `json:"window_start"`
	WindowEnd          string   `json:"window_end"`
}

// FirmwareReport is one firmware version observed for a device, with first/last seen.
type FirmwareReport struct {
	Version   string `json:"version"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

// TelemetryStore persists samples and serves the device-detail read paths.
type TelemetryStore interface {
	SaveSample(ctx context.Context, sample TelemetrySample) error
	LatestSample(ctx context.Context, tenantID, deviceID string) (*TelemetrySample, bool, error)
	SampleHistory(ctx context.Context, tenantID, deviceID string, since time.Time, limit int) ([]TelemetrySample, error)
	Aggregates(ctx context.Context, tenantID, deviceID string, since, until time.Time) (TelemetryAggregates, error)
	FirmwareHistory(ctx context.Context, tenantID, deviceID string) ([]FirmwareReport, error)
}

type DeviceHealth string

const (
	DeviceHealthOnline   DeviceHealth = "online"
	DeviceHealthDegraded DeviceHealth = "degraded"
	DeviceHealthOffline  DeviceHealth = "offline"
	DeviceHealthUnknown  DeviceHealth = "unknown"
)

// DeriveHealth maps time-since-last-contact to a health band: online within one
// status-timeout window, degraded within three, offline beyond, unknown if the
// device has never reported.
func DeriveHealth(lastContact time.Time, now time.Time, statusTimeoutSeconds int) DeviceHealth {
	if lastContact.IsZero() {
		return DeviceHealthUnknown
	}
	if statusTimeoutSeconds <= 0 {
		statusTimeoutSeconds = 600
	}
	timeout := time.Duration(statusTimeoutSeconds) * time.Second
	since := now.Sub(lastContact)
	switch {
	case since <= timeout:
		return DeviceHealthOnline
	case since <= 3*timeout:
		return DeviceHealthDegraded
	default:
		return DeviceHealthOffline
	}
}
