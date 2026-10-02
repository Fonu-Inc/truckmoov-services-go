package application

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	quarantineFlushEvery = 10 * time.Second
	quarantineMaxPending = 50000
	QuarantineListLimit  = 500
)

type QuarantinedDevice struct {
	IMEI         string    `json:"imei"`
	FirstSeenAt  time.Time `json:"firstSeenAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
	MessageCount int       `json:"messageCount"`
}

type QuarantineStore interface {
	Record(ctx context.Context, imei string, count int, at time.Time) error
	List(ctx context.Context, limit int) ([]QuarantinedDevice, error)
	Remove(ctx context.Context, imei string) error
}

type pendingQuarantine struct {
	count     int
	lastFlush time.Time
}

// Quarantine keeps a minimal record of senders whose IMEI nobody registered.
// Writes are batched per IMEI so a chatty unknown device costs one upsert per
// flush window, not one per message.
type Quarantine struct {
	store   QuarantineStore
	clock   Clock
	mu      sync.Mutex
	pending map[string]*pendingQuarantine
}

func NewQuarantine(store QuarantineStore, clock Clock) *Quarantine {
	return &Quarantine{store: store, clock: clock, pending: map[string]*pendingQuarantine{}}
}

func (q *Quarantine) Observe(ctx context.Context, imei string) {
	imei = strings.TrimSpace(imei)
	if q == nil || q.store == nil || imei == "" {
		return
	}
	now := q.clock.Now()
	count, due := q.take(imei, now)
	if !due {
		return
	}
	if err := q.store.Record(ctx, imei, count, now); err != nil {
		q.giveBack(imei, count)
	}
}

func (q *Quarantine) take(imei string, now time.Time) (int, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, ok := q.pending[imei]
	if !ok {
		if len(q.pending) >= quarantineMaxPending {
			return 0, false
		}
		entry = &pendingQuarantine{}
		q.pending[imei] = entry
	}
	entry.count++
	if !entry.lastFlush.IsZero() && now.Sub(entry.lastFlush) < quarantineFlushEvery {
		return 0, false
	}
	count := entry.count
	entry.count = 0
	entry.lastFlush = now
	return count, true
}

func (q *Quarantine) giveBack(imei string, count int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if entry, ok := q.pending[imei]; ok {
		entry.count += count
		entry.lastFlush = time.Time{}
	}
}

func (q *Quarantine) List(ctx context.Context) ([]QuarantinedDevice, error) {
	if q == nil || q.store == nil {
		return []QuarantinedDevice{}, nil
	}
	devices, err := q.store.List(ctx, QuarantineListLimit)
	if err != nil {
		return nil, err
	}
	if devices == nil {
		devices = []QuarantinedDevice{}
	}
	return devices, nil
}

func (q *Quarantine) Clear(ctx context.Context, imei string) {
	imei = strings.TrimSpace(imei)
	if q == nil || q.store == nil || imei == "" {
		return
	}
	q.mu.Lock()
	delete(q.pending, imei)
	q.mu.Unlock()
	_ = q.store.Remove(ctx, imei)
}
