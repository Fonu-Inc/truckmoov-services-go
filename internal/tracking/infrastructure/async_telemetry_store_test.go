package infrastructure

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

type blockingTelemetryStore struct {
	mu     sync.Mutex
	saved  int
	block  chan struct{}
	resume chan struct{}
}

func (b *blockingTelemetryStore) SaveSample(_ context.Context, _ application.TelemetrySample) error {
	b.block <- struct{}{}
	<-b.resume
	b.mu.Lock()
	b.saved++
	b.mu.Unlock()
	return nil
}

func (b *blockingTelemetryStore) LatestSample(context.Context, string, string) (*application.TelemetrySample, bool, error) {
	return nil, false, nil
}
func (b *blockingTelemetryStore) SampleHistory(context.Context, string, string, time.Time, int) ([]application.TelemetrySample, error) {
	return nil, nil
}
func (b *blockingTelemetryStore) Aggregates(context.Context, string, string, time.Time, time.Time) (application.TelemetryAggregates, error) {
	return application.TelemetryAggregates{}, nil
}
func (b *blockingTelemetryStore) FirmwareHistory(context.Context, string, string) ([]application.FirmwareReport, error) {
	return nil, nil
}

func TestAsyncTelemetryStoreDoesNotBlockCaller(t *testing.T) {
	inner := &blockingTelemetryStore{
		block:  make(chan struct{}, 1),
		resume: make(chan struct{}),
	}
	store := NewAsyncTelemetryStore(inner, 1, nil)

	started := time.Now()
	if err := store.SaveSample(context.Background(), application.TelemetrySample{DeviceID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 50*time.Millisecond {
		t.Fatalf("SaveSample blocked the caller")
	}

	select {
	case <-inner.block:
	case <-time.After(time.Second):
		t.Fatal("inner SaveSample never started")
	}
	close(inner.resume)
}
