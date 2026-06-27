package infrastructure

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

// InMemoryTelemetryRepository is a test/dev TelemetryStore with no external deps.
type InMemoryTelemetryRepository struct {
	mu      sync.RWMutex
	samples []application.TelemetrySample
}

func NewInMemoryTelemetryRepository() *InMemoryTelemetryRepository {
	return &InMemoryTelemetryRepository{}
}

func (r *InMemoryTelemetryRepository) SaveSample(_ context.Context, s application.TelemetrySample) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, s)
	return nil
}

func (r *InMemoryTelemetryRepository) LatestSample(_ context.Context, tenantID, deviceID string) (*application.TelemetrySample, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var latest *application.TelemetrySample
	for i := range r.samples {
		s := r.samples[i]
		if s.TenantID != tenantID || s.DeviceID != deviceID {
			continue
		}
		if latest == nil || s.ObservedAt.After(latest.ObservedAt) {
			copy := s
			latest = &copy
		}
	}
	if latest == nil {
		return nil, false, nil
	}
	return latest, true, nil
}

func (r *InMemoryTelemetryRepository) SampleHistory(_ context.Context, tenantID, deviceID string, since time.Time, limit int) ([]application.TelemetrySample, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]application.TelemetrySample, 0)
	for _, s := range r.samples {
		if s.TenantID != tenantID || s.DeviceID != deviceID {
			continue
		}
		if s.ObservedAt.Before(since) {
			continue
		}
		matched = append(matched, s)
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].ObservedAt.After(matched[j].ObservedAt)
	})
	limit = normalizeHistoryLimit(limit)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (r *InMemoryTelemetryRepository) Aggregates(_ context.Context, tenantID, deviceID string, since, until time.Time) (application.TelemetryAggregates, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var count int
	var min, max time.Time
	for _, s := range r.samples {
		if s.TenantID != tenantID || s.DeviceID != deviceID {
			continue
		}
		if s.ObservedAt.Before(since) || !s.ObservedAt.Before(until) {
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

	agg := application.TelemetryAggregates{
		SampleCount: count,
		WindowStart: since.UTC().Format(time.RFC3339),
		WindowEnd:   until.UTC().Format(time.RFC3339),
	}
	if count > 1 {
		avg := max.Sub(min).Seconds() / float64(count-1)
		agg.AvgIntervalSeconds = &avg
	}
	return agg, nil
}

func (r *InMemoryTelemetryRepository) FirmwareHistory(_ context.Context, tenantID, deviceID string) ([]application.FirmwareReport, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	type span struct{ first, last time.Time }
	seen := make(map[string]*span)
	for _, s := range r.samples {
		if s.TenantID != tenantID || s.DeviceID != deviceID || s.FirmwareVersion == "" {
			continue
		}
		sp, ok := seen[s.FirmwareVersion]
		if !ok {
			seen[s.FirmwareVersion] = &span{first: s.ObservedAt, last: s.ObservedAt}
			continue
		}
		if s.ObservedAt.Before(sp.first) {
			sp.first = s.ObservedAt
		}
		if s.ObservedAt.After(sp.last) {
			sp.last = s.ObservedAt
		}
	}

	reports := make([]application.FirmwareReport, 0, len(seen))
	for version, sp := range seen {
		reports = append(reports, application.FirmwareReport{
			Version:   version,
			FirstSeen: sp.first.UTC().Format(time.RFC3339),
			LastSeen:  sp.last.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].LastSeen > reports[j].LastSeen
	})
	return reports, nil
}
