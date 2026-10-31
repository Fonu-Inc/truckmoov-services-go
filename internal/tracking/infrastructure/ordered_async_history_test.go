package infrastructure

import (
	"sync"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

func TestOrderedAsyncHistoryPreservesPerVehicleOrder(t *testing.T) {
	inner := NewInMemoryLocationHistoryRepository()
	repo := NewOrderedAsyncHistoryRepository(inner, nil)

	const n = 40
	for i := 0; i < n; i++ {
		if err := repo.Append(application.LatestLocation{
			TenantID:   "t1",
			VehicleID:  "v1",
			FixTimeRFC: time.Unix(int64(i), 0).UTC().Format(time.RFC3339),
			EventID:    time.Unix(int64(i), 0).UTC().Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	var hist []application.LatestLocation
	for {
		var err error
		hist, err = repo.History("t1", "v1", n)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(hist) == n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for history drain, got %d/%d", len(hist), n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Newest-first: first entry must be the last appended fix.
	if hist[0].FixTimeRFC != time.Unix(n-1, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("expected newest head, got %s", hist[0].FixTimeRFC)
	}
	for i := 0; i < n-1; i++ {
		newer := hist[i].FixTimeRFC
		older := hist[i+1].FixTimeRFC
		if newer <= older {
			t.Fatalf("order broken at %d: %s then %s", i, newer, older)
		}
	}
}

func TestOrderedAsyncHistoryDoesNotBlockCaller(t *testing.T) {
	block := make(chan struct{})
	inner := &blockingHistory{block: block}
	repo := NewOrderedAsyncHistoryRepository(inner, nil)

	done := make(chan struct{})
	go func() {
		_ = repo.Append(application.LatestLocation{TenantID: "t", VehicleID: "v"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Append blocked on slow inner store")
	}
	close(block)
}

type blockingHistory struct {
	block chan struct{}
	mu    sync.Mutex
	n     int
}

func (b *blockingHistory) Append(application.LatestLocation) error {
	<-b.block
	b.mu.Lock()
	b.n++
	b.mu.Unlock()
	return nil
}

func (b *blockingHistory) History(string, string, int) ([]application.LatestLocation, error) {
	return nil, nil
}
