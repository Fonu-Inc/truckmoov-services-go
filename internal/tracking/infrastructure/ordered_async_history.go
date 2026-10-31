package infrastructure

import (
	"log/slog"
	"sync"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

// OrderedAsyncHistoryRepository takes history Append off the ingest hot path
// while preserving newest-first order per vehicle: each key has one worker
// that drains a buffered channel in call order.
type OrderedAsyncHistoryRepository struct {
	inner   application.LocationHistoryRepository
	queues  sync.Map // string -> *historyQueue
	log     *slog.Logger
	bufSize int
}

type historyQueue struct {
	ch   chan application.LatestLocation
	once sync.Once
}

func NewOrderedAsyncHistoryRepository(inner application.LocationHistoryRepository, log *slog.Logger) *OrderedAsyncHistoryRepository {
	return &OrderedAsyncHistoryRepository{inner: inner, log: log, bufSize: 64}
}

func (a *OrderedAsyncHistoryRepository) Append(entry application.LatestLocation) error {
	key := entry.TenantID + ":" + entry.VehicleID
	raw, _ := a.queues.LoadOrStore(key, &historyQueue{ch: make(chan application.LatestLocation, a.bufSize)})
	q := raw.(*historyQueue)
	q.once.Do(func() {
		go a.drain(key, q.ch)
	})
	select {
	case q.ch <- entry:
	default:
		// shortcut: drop under backpressure; latest snapshot already in Redis
		if a.log != nil {
			a.log.Warn("location_history_dropped", "tenant", entry.TenantID, "vehicle", entry.VehicleID)
		}
	}
	return nil
}

func (a *OrderedAsyncHistoryRepository) drain(key string, ch <-chan application.LatestLocation) {
	for entry := range ch {
		if err := a.inner.Append(entry); err != nil && a.log != nil {
			a.log.Warn("location_history_append_failed", "key", key, "error", err)
		}
	}
}

func (a *OrderedAsyncHistoryRepository) History(tenantID, vehicleID string, limit int) ([]application.LatestLocation, error) {
	return a.inner.History(tenantID, vehicleID, limit)
}
