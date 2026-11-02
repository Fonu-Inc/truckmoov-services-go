package infrastructure

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

const asyncTelemetryWriteTimeout = 2 * time.Second
const telemetryDropLogEvery = 5 * time.Second

// AsyncTelemetryStore decouples Timescale writes from the live ingest path.
// Redis + NATS remain the source of truth; samples are queued with a hard
// in-flight cap so a slow DB cannot stall GPS responses.
type AsyncTelemetryStore struct {
	inner   application.TelemetryStore
	workers chan struct{}
	log     *slog.Logger

	logMu        sync.Mutex
	lastDropLog  time.Time
	droppedSince int
}

func NewAsyncTelemetryStore(inner application.TelemetryStore, maxInFlight int, log *slog.Logger) *AsyncTelemetryStore {
	if maxInFlight <= 0 {
		maxInFlight = 64
	}
	return &AsyncTelemetryStore{
		inner:   inner,
		workers: make(chan struct{}, maxInFlight),
		log:     log,
	}
}

// recordDrop logs a dropped sample at most once per interval. Under a DB
// outage the drop rate is the ingest rate; one warning per sample would flood
// stdout/Loki and can block the very handler this store is meant to protect.
func (a *AsyncTelemetryStore) recordDrop(deviceID string) {
	if a.log == nil {
		return
	}
	a.logMu.Lock()
	a.droppedSince++
	if time.Since(a.lastDropLog) < telemetryDropLogEvery {
		a.logMu.Unlock()
		return
	}
	dropped := a.droppedSince
	a.droppedSince = 0
	a.lastDropLog = time.Now()
	a.logMu.Unlock()

	a.log.Warn("telemetry_sample_dropped", "device_id", deviceID, "dropped_since_last_log", dropped)
}

func (a *AsyncTelemetryStore) SaveSample(_ context.Context, sample application.TelemetrySample) error {
	select {
	case a.workers <- struct{}{}:
		go func() {
			defer func() { <-a.workers }()
			ctx, cancel := context.WithTimeout(context.Background(), asyncTelemetryWriteTimeout)
			defer cancel()
			_ = a.inner.SaveSample(ctx, sample)
		}()
	default:
		// shortcut: drop under backpressure; live path already persisted in Redis/NATS
		a.recordDrop(sample.DeviceID)
	}
	return nil
}

func (a *AsyncTelemetryStore) LatestSample(ctx context.Context, tenantID, deviceID string) (*application.TelemetrySample, bool, error) {
	return a.inner.LatestSample(ctx, tenantID, deviceID)
}

func (a *AsyncTelemetryStore) SampleHistory(ctx context.Context, tenantID, deviceID string, since time.Time, limit int) ([]application.TelemetrySample, error) {
	return a.inner.SampleHistory(ctx, tenantID, deviceID, since, limit)
}

func (a *AsyncTelemetryStore) Aggregates(ctx context.Context, tenantID, deviceID string, since, until time.Time) (application.TelemetryAggregates, error) {
	return a.inner.Aggregates(ctx, tenantID, deviceID, since, until)
}

func (a *AsyncTelemetryStore) FirmwareHistory(ctx context.Context, tenantID, deviceID string) ([]application.FirmwareReport, error) {
	return a.inner.FirmwareHistory(ctx, tenantID, deviceID)
}
