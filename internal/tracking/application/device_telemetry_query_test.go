package application

import (
	"context"
	"sort"
	"testing"
	"time"
)

type fakeTelemetryStore struct {
	samples []TelemetrySample
}

func (f *fakeTelemetryStore) SaveSample(_ context.Context, s TelemetrySample) error {
	f.samples = append(f.samples, s)
	return nil
}

func (f *fakeTelemetryStore) LatestSample(_ context.Context, tenantID, deviceID string) (*TelemetrySample, bool, error) {
	var latest *TelemetrySample
	for i := range f.samples {
		s := f.samples[i]
		if s.TenantID != tenantID || s.DeviceID != deviceID {
			continue
		}
		if latest == nil || s.ObservedAt.After(latest.ObservedAt) {
			cp := s
			latest = &cp
		}
	}
	if latest == nil {
		return nil, false, nil
	}
	return latest, true, nil
}

func (f *fakeTelemetryStore) SampleHistory(_ context.Context, tenantID, deviceID string, since time.Time, limit int) ([]TelemetrySample, error) {
	out := make([]TelemetrySample, 0)
	for _, s := range f.samples {
		if s.TenantID == tenantID && s.DeviceID == deviceID && !s.ObservedAt.Before(since) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObservedAt.After(out[j].ObservedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeTelemetryStore) Aggregates(_ context.Context, tenantID, deviceID string, since, until time.Time) (TelemetryAggregates, error) {
	var count int
	var min, max time.Time
	for _, s := range f.samples {
		if s.TenantID != tenantID || s.DeviceID != deviceID || s.ObservedAt.Before(since) || !s.ObservedAt.Before(until) {
			continue
		}
		count++
		if min.IsZero() || s.ObservedAt.Before(min) {
			min = s.ObservedAt
		}
		if s.ObservedAt.After(max) {
			max = s.ObservedAt
		}
	}
	agg := TelemetryAggregates{SampleCount: count}
	if count > 1 {
		avg := max.Sub(min).Seconds() / float64(count-1)
		agg.AvgIntervalSeconds = &avg
	}
	return agg, nil
}

func (f *fakeTelemetryStore) FirmwareHistory(_ context.Context, tenantID, deviceID string) ([]FirmwareReport, error) {
	seen := map[string]bool{}
	out := make([]FirmwareReport, 0)
	for _, s := range f.samples {
		if s.TenantID == tenantID && s.DeviceID == deviceID && s.FirmwareVersion != "" && !seen[s.FirmwareVersion] {
			seen[s.FirmwareVersion] = true
			out = append(out, FirmwareReport{Version: s.FirmwareVersion})
		}
	}
	return out, nil
}

func TestDeriveHealth(t *testing.T) {
	now := time.Date(2026, 2, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		want DeviceHealth
	}{
		{"online", now.Add(-5 * time.Minute), DeviceHealthOnline},
		{"degraded", now.Add(-25 * time.Minute), DeviceHealthDegraded},
		{"offline", now.Add(-40 * time.Minute), DeviceHealthOffline},
		{"unknown", time.Time{}, DeviceHealthUnknown},
	}
	for _, c := range cases {
		if got := DeriveHealth(c.last, now, 600); got != c.want {
			t.Fatalf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func seededQueryUseCase() (*DeviceTelemetryQueryUseCase, time.Time) {
	now := time.Date(2026, 2, 2, 12, 0, 0, 0, time.UTC)
	store := &fakeTelemetryStore{}
	sig1, bat1 := -70.0, 80.0
	sig2, bat2 := -65.0, 78.0
	_ = store.SaveSample(context.Background(), TelemetrySample{
		TenantID: "t1", DeviceID: "d1", VehicleID: "v1", IMEI: "359779090012345",
		Latitude: 6.5, Longitude: 3.4, SignalStrength: &sig1, BatteryLevel: &bat1,
		FirmwareVersion: "1.0.0", ObservedAt: now.Add(-5 * time.Minute),
	})
	_ = store.SaveSample(context.Background(), TelemetrySample{
		TenantID: "t1", DeviceID: "d1", VehicleID: "v1", IMEI: "359779090012345",
		Latitude: 6.6, Longitude: 3.5, SignalStrength: &sig2, BatteryLevel: &bat2,
		FirmwareVersion: "1.0.0", ObservedAt: now.Add(-2 * time.Minute),
	})
	return NewDeviceTelemetryQueryUseCase(store, fixedTrackingClock{now: now}, 600), now
}

func TestDeviceQuery_OverviewAndConnectivity(t *testing.T) {
	uc, _ := seededQueryUseCase()

	overview, err := uc.Overview(context.Background(), "t1", "d1")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.Health != string(DeviceHealthOnline) {
		t.Fatalf("expected online, got %s", overview.Health)
	}
	if overview.BatteryLevel == nil || *overview.BatteryLevel != 78.0 {
		t.Fatalf("expected latest battery 78, got %v", overview.BatteryLevel)
	}
	if overview.VehicleID != "v1" {
		t.Fatalf("expected vehicle v1, got %s", overview.VehicleID)
	}

	conn, err := uc.Connectivity(context.Background(), "t1", "d1")
	if err != nil {
		t.Fatalf("connectivity: %v", err)
	}
	if conn.SampleCount24h != 2 {
		t.Fatalf("expected 2 samples, got %d", conn.SampleCount24h)
	}
	if conn.ReportingIntervalSeconds == nil || *conn.ReportingIntervalSeconds != 180 {
		t.Fatalf("expected 180s interval, got %v", conn.ReportingIntervalSeconds)
	}
}

func TestDeviceQuery_TelemetryAndFirmware(t *testing.T) {
	uc, _ := seededQueryUseCase()

	tel, err := uc.Telemetry(context.Background(), "t1", "d1", 100)
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	if tel.Latest == nil || len(tel.History) != 2 {
		t.Fatalf("expected latest + 2 history, got %v / %d", tel.Latest, len(tel.History))
	}
	if tel.Aggregates.DataQualityPct == nil {
		t.Fatalf("expected data quality computed")
	}

	fw, err := uc.Firmware(context.Background(), "t1", "d1")
	if err != nil {
		t.Fatalf("firmware: %v", err)
	}
	if fw.CurrentVersion != "1.0.0" || len(fw.History) != 1 {
		t.Fatalf("expected current 1.0.0 + 1 history, got %s / %d", fw.CurrentVersion, len(fw.History))
	}
}

func TestDeviceQuery_NilStoreDegrades(t *testing.T) {
	uc := NewDeviceTelemetryQueryUseCase(nil, fixedTrackingClock{now: time.Now().UTC()}, 600)
	ov, err := uc.Overview(context.Background(), "t1", "d1")
	if err != nil || ov.Health != string(DeviceHealthUnknown) {
		t.Fatalf("expected unknown health on nil store, got %s err %v", ov.Health, err)
	}
}
