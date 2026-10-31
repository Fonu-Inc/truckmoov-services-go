package infrastructure

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

const telemetrySamplesSchema = `
CREATE TABLE IF NOT EXISTS telemetry_samples (
    tenant_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    vehicle_id TEXT NOT NULL DEFAULT '',
    imei TEXT NOT NULL DEFAULT '',
    observed_at TIMESTAMPTZ NOT NULL,
    fix_time TIMESTAMPTZ NULL,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    speed_kph DOUBLE PRECISION NULL,
    heading DOUBLE PRECISION NULL,
    accuracy_m DOUBLE PRECISION NULL,
    battery_level DOUBLE PRECISION NULL,
    signal_strength DOUBLE PRECISION NULL,
    fuel_level DOUBLE PRECISION NULL,
    engine_on BOOLEAN NULL,
    firmware_version TEXT NOT NULL DEFAULT '',
    event_id TEXT NOT NULL DEFAULT '',
    protocol TEXT NOT NULL DEFAULT ''
);

ALTER TABLE telemetry_samples ADD COLUMN IF NOT EXISTS protocol TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_telemetry_device_time
    ON telemetry_samples (tenant_id, device_id, observed_at DESC);

CREATE INDEX IF NOT EXISTS idx_telemetry_device_firmware
    ON telemetry_samples (tenant_id, device_id, firmware_version);
`

type TimescaleTelemetryRepository struct {
	db            *sql.DB
	log           *slog.Logger
	retentionDays int
}

func NewTimescaleTelemetryRepository(db *sql.DB, log *slog.Logger, retentionDays int) *TimescaleTelemetryRepository {
	return &TimescaleTelemetryRepository{db: db, log: log, retentionDays: retentionDays}
}

// EnsureSchema creates the table + indexes (mandatory) then promotes it to a
// hypertable and attaches a retention policy (best-effort). The best-effort step
// lets the same code run against a plain Postgres in local dev, where the table
// simply degrades to a regular relation.
func (r *TimescaleTelemetryRepository) EnsureSchema(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, telemetrySamplesSchema); err != nil {
		return err
	}

	// 1-day chunks keep recent device-detail queries scanning a small slice
	// instead of a default week-sized chunk under high write rates.
	if _, err := r.db.ExecContext(ctx,
		`SELECT create_hypertable('telemetry_samples', 'observed_at',
			chunk_time_interval => INTERVAL '1 day', if_not_exists => TRUE)`,
	); err != nil {
		r.log.Warn("telemetry_hypertable_skipped", "error", err.Error())
		return nil
	}

	if r.retentionDays > 0 {
		if _, err := r.db.ExecContext(ctx,
			`SELECT add_retention_policy('telemetry_samples', make_interval(days => $1), if_not_exists => TRUE)`,
			r.retentionDays,
		); err != nil {
			r.log.Warn("telemetry_retention_skipped", "error", err.Error())
		}
	}

	// Compress cold chunks so history/aggregate reads hit less disk while
	// ingest keeps writing uncompressed recent chunks.
	if _, err := r.db.ExecContext(ctx, `
ALTER TABLE telemetry_samples SET (
  timescaledb.compress,
  timescaledb.compress_segmentby = 'tenant_id,device_id',
  timescaledb.compress_orderby = 'observed_at DESC'
)`); err != nil {
		r.log.Warn("telemetry_compress_skipped", "error", err.Error())
	} else if _, err := r.db.ExecContext(ctx,
		`SELECT add_compression_policy('telemetry_samples', INTERVAL '7 days', if_not_exists => TRUE)`,
	); err != nil {
		r.log.Warn("telemetry_compress_policy_skipped", "error", err.Error())
	}
	return nil
}

func (r *TimescaleTelemetryRepository) SaveSample(ctx context.Context, s application.TelemetrySample) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO telemetry_samples (
    tenant_id, device_id, vehicle_id, imei, observed_at, fix_time,
    latitude, longitude, speed_kph, heading, accuracy_m,
    battery_level, signal_strength, fuel_level, engine_on, firmware_version, event_id, protocol
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		s.TenantID, s.DeviceID, s.VehicleID, s.IMEI, s.ObservedAt, nullTime(s.FixTime),
		s.Latitude, s.Longitude, nullFloat(s.SpeedKPH), nullFloat(s.Heading), nullFloat(s.AccuracyM),
		nullFloat(s.BatteryLevel), nullFloat(s.SignalStrength), nullFloat(s.FuelLevel), nullBool(s.EngineOn),
		s.FirmwareVersion, s.EventID, s.Protocol,
	)
	if err != nil {
		// Surfaced here because the ingest caller intentionally discards the error to
		// keep the live path non-failing; this is the only observability for a
		// persistent write failure (disk/connection/schema drift).
		r.log.Warn("telemetry_sample_save_failed", "device_id", s.DeviceID, "error", err.Error())
	}
	return err
}

const telemetryColumns = `tenant_id, device_id, vehicle_id, imei, observed_at, fix_time,
    latitude, longitude, speed_kph, heading, accuracy_m,
    battery_level, signal_strength, fuel_level, engine_on, firmware_version, event_id, protocol`

func (r *TimescaleTelemetryRepository) LatestSample(ctx context.Context, tenantID, deviceID string) (*application.TelemetrySample, bool, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+telemetryColumns+` FROM telemetry_samples
		 WHERE tenant_id = $1 AND device_id = $2 ORDER BY observed_at DESC LIMIT 1`,
		tenantID, deviceID,
	)
	sample, err := scanSample(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &sample, true, nil
}

// normalizeHistoryLimit bounds a requested history page: unset/oversized → 1000.
// Shared by both stores so the in-memory test store matches SQL semantics.
func normalizeHistoryLimit(limit int) int {
	if limit <= 0 || limit > 5000 {
		return 1000
	}
	return limit
}

func (r *TimescaleTelemetryRepository) SampleHistory(ctx context.Context, tenantID, deviceID string, since time.Time, limit int) ([]application.TelemetrySample, error) {
	limit = normalizeHistoryLimit(limit)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+telemetryColumns+` FROM telemetry_samples
		 WHERE tenant_id = $1 AND device_id = $2 AND observed_at >= $3
		 ORDER BY observed_at DESC LIMIT $4`,
		tenantID, deviceID, since, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	samples := make([]application.TelemetrySample, 0, limit)
	for rows.Next() {
		sample, err := scanSample(rows)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

func (r *TimescaleTelemetryRepository) Aggregates(ctx context.Context, tenantID, deviceID string, since, until time.Time) (application.TelemetryAggregates, error) {
	var count int
	var span sql.NullFloat64 // seconds between first and last sample in window
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        EXTRACT(EPOCH FROM (MAX(observed_at) - MIN(observed_at)))
		 FROM telemetry_samples
		 WHERE tenant_id = $1 AND device_id = $2 AND observed_at >= $3 AND observed_at < $4`,
		tenantID, deviceID, since, until,
	).Scan(&count, &span)
	if err != nil {
		return application.TelemetryAggregates{}, err
	}

	agg := application.TelemetryAggregates{
		SampleCount: count,
		WindowStart: since.UTC().Format(time.RFC3339),
		WindowEnd:   until.UTC().Format(time.RFC3339),
	}
	if count > 1 && span.Valid {
		avg := span.Float64 / float64(count-1)
		agg.AvgIntervalSeconds = &avg
	}
	return agg, nil
}

func (r *TimescaleTelemetryRepository) FirmwareHistory(ctx context.Context, tenantID, deviceID string) ([]application.FirmwareReport, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT firmware_version, MIN(observed_at), MAX(observed_at)
		 FROM telemetry_samples
		 WHERE tenant_id = $1 AND device_id = $2 AND firmware_version <> ''
		 GROUP BY firmware_version ORDER BY MAX(observed_at) DESC`,
		tenantID, deviceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reports := make([]application.FirmwareReport, 0)
	for rows.Next() {
		var version string
		var first, last time.Time
		if err := rows.Scan(&version, &first, &last); err != nil {
			return nil, err
		}
		reports = append(reports, application.FirmwareReport{
			Version:   version,
			FirstSeen: first.UTC().Format(time.RFC3339),
			LastSeen:  last.UTC().Format(time.RFC3339),
		})
	}
	return reports, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSample(row rowScanner) (application.TelemetrySample, error) {
	var s application.TelemetrySample
	var fixTime sql.NullTime
	var speed, heading, accuracy, battery, signal, fuel sql.NullFloat64
	var engineOn sql.NullBool

	if err := row.Scan(
		&s.TenantID, &s.DeviceID, &s.VehicleID, &s.IMEI, &s.ObservedAt, &fixTime,
		&s.Latitude, &s.Longitude, &speed, &heading, &accuracy,
		&battery, &signal, &fuel, &engineOn, &s.FirmwareVersion, &s.EventID, &s.Protocol,
	); err != nil {
		return application.TelemetrySample{}, err
	}

	if fixTime.Valid {
		s.FixTime = &fixTime.Time
	}
	s.SpeedKPH = floatPtr(speed)
	s.Heading = floatPtr(heading)
	s.AccuracyM = floatPtr(accuracy)
	s.BatteryLevel = floatPtr(battery)
	s.SignalStrength = floatPtr(signal)
	s.FuelLevel = floatPtr(fuel)
	if engineOn.Valid {
		s.EngineOn = &engineOn.Bool
	}
	return s, nil
}

func nullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

func nullBool(v *bool) sql.NullBool {
	if v == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *v, Valid: true}
}

func nullTime(v *time.Time) sql.NullTime {
	if v == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *v, Valid: true}
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}
