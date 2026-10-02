package application

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	quarantineFlushEvery     = 10 * time.Second
	quarantineMaxPending     = 50000
	quarantineMaxIMEILen     = 64
	QuarantineListLimit      = 500
	QuarantineRetention      = 30 * 24 * time.Hour
	quarantineAlertThreshold = 1000
	quarantineAlertWindow    = time.Hour
	quarantineReloadEvery    = time.Minute
	quarantinePruneEvery     = 24 * time.Hour

	QuarantineActive    = "active"
	QuarantineDismissed = "dismissed"
	QuarantineBlocked   = "blocked"
)

var (
	ErrQuarantineNotFound   = errors.New("unrecognised device not found")
	ErrQuarantineBlocked    = errors.New("imei is blocked")
	ErrQuarantineNotBlocked = errors.New("imei is not blocked")
)

type QuarantinedDevice struct {
	IMEI         string     `json:"imei"`
	FirstSeenAt  time.Time  `json:"firstSeenAt"`
	LastSeenAt   time.Time  `json:"lastSeenAt"`
	MessageCount int        `json:"messageCount"`
	Status       string     `json:"status"`
	HandledAt    *time.Time `json:"handledAt,omitempty"`
}

type QuarantineStore interface {
	Record(ctx context.Context, imei string, count int, at time.Time) error
	List(ctx context.Context, limit int) ([]QuarantinedDevice, error)
	Remove(ctx context.Context, imei string) error
	SetStatus(ctx context.Context, imei, status string, at time.Time) error
	BlockedIMEIs(ctx context.Context) ([]string, error)
	Prune(ctx context.Context, cutoff time.Time) (int, error)
}

// QuarantineAlerter tells platform staff that one unknown IMEI is flooding
// ingest.
type QuarantineAlerter interface {
	NoisyIMEI(ctx context.Context, imei string, count int)
}

type pendingQuarantine struct {
	count     int
	lastFlush time.Time
}

type hourWindow struct {
	start   time.Time
	count   int
	alerted bool
}

// Quarantine keeps a minimal record of senders whose IMEI nobody registered.
// Writes are batched per IMEI so a chatty unknown device costs one upsert per
// flush window, not one per message.
type Quarantine struct {
	store   QuarantineStore
	clock   Clock
	alerter QuarantineAlerter
	log     *slog.Logger
	mu      sync.Mutex
	pending map[string]*pendingQuarantine
	hourly  map[string]*hourWindow
	blocked map[string]struct{}
}

func NewQuarantine(store QuarantineStore, clock Clock) *Quarantine {
	return &Quarantine{
		store:   store,
		clock:   clock,
		pending: map[string]*pendingQuarantine{},
		hourly:  map[string]*hourWindow{},
		blocked: map[string]struct{}{},
	}
}

func (q *Quarantine) WithAlerter(alerter QuarantineAlerter) *Quarantine {
	q.alerter = alerter
	return q
}

func (q *Quarantine) WithLogger(log *slog.Logger) *Quarantine {
	q.log = log
	return q
}

func (q *Quarantine) Observe(ctx context.Context, imei string) {
	imei = strings.TrimSpace(imei)
	if q == nil || q.store == nil || imei == "" || len(imei) > quarantineMaxIMEILen {
		return
	}
	now := q.clock.Now()
	if q.noisy(imei, now) && q.alerter != nil {
		q.alerter.NoisyIMEI(ctx, imei, quarantineAlertThreshold)
	}
	count, due := q.take(imei, now)
	if !due {
		return
	}
	if err := q.store.Record(ctx, imei, count, now); err != nil {
		q.giveBack(imei, count)
	}
}

// noisy is true once per window, on the message that crosses the threshold.
func (q *Quarantine) noisy(imei string, now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	window, ok := q.hourly[imei]
	if !ok {
		if len(q.hourly) >= quarantineMaxPending {
			q.dropStaleWindows(now)
		}
		window = &hourWindow{start: now}
		q.hourly[imei] = window
	}
	if now.Sub(window.start) >= quarantineAlertWindow {
		*window = hourWindow{start: now}
	}
	window.count++
	if window.alerted || window.count <= quarantineAlertThreshold {
		return false
	}
	window.alerted = true
	return true
}

func (q *Quarantine) dropStaleWindows(now time.Time) {
	for imei, window := range q.hourly {
		if now.Sub(window.start) >= quarantineAlertWindow {
			delete(q.hourly, imei)
		}
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
	delete(q.hourly, imei)
	delete(q.blocked, imei)
	q.mu.Unlock()
	_ = q.store.Remove(ctx, imei)
}

// IsBlocked reports a staff block on an unknown IMEI. Blocked senders are
// refused at ingest and never recorded again.
func (q *Quarantine) IsBlocked(imei string) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	_, blocked := q.blocked[strings.TrimSpace(imei)]
	return blocked
}

func (q *Quarantine) Dismiss(ctx context.Context, imei string) error {
	return q.setStatus(ctx, imei, QuarantineDismissed)
}

func (q *Quarantine) Block(ctx context.Context, imei string) error {
	return q.setStatus(ctx, imei, QuarantineBlocked)
}

func (q *Quarantine) Unblock(ctx context.Context, imei string) error {
	if err := q.setStatus(ctx, imei, QuarantineActive); err != nil {
		return err
	}
	_ = q.Reload(ctx)
	return nil
}

func (q *Quarantine) setStatus(ctx context.Context, imei, status string) error {
	imei = strings.TrimSpace(imei)
	if q == nil || q.store == nil || imei == "" || len(imei) > quarantineMaxIMEILen {
		return ErrQuarantineNotFound
	}
	if err := q.store.SetStatus(ctx, imei, status, q.clock.Now()); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if status == QuarantineBlocked {
		q.blocked[imei] = struct{}{}
		return nil
	}
	delete(q.blocked, imei)
	return nil
}

// Reload refreshes the blocked set from the store so a block made on another
// instance takes effect here too.
func (q *Quarantine) Reload(ctx context.Context) error {
	if q == nil || q.store == nil {
		return nil
	}
	imeis, err := q.store.BlockedIMEIs(ctx)
	if err != nil {
		return err
	}
	set := make(map[string]struct{}, len(imeis))
	for _, imei := range imeis {
		set[imei] = struct{}{}
	}
	q.mu.Lock()
	q.blocked = set
	q.mu.Unlock()
	return nil
}

// Prune deletes rows idle or dismissed for longer than the retention window.
// Blocked rows are kept, since deleting one would lift the block.
func (q *Quarantine) Prune(ctx context.Context) (int, error) {
	if q == nil || q.store == nil {
		return 0, nil
	}
	return q.store.Prune(ctx, q.clock.Now().Add(-QuarantineRetention))
}

// Run loads the blocked set and prunes at start, then reloads every minute and
// prunes daily until ctx ends.
func (q *Quarantine) Run(ctx context.Context) {
	if q == nil || q.store == nil {
		return
	}
	q.reloadLogged(ctx)
	q.pruneLogged(ctx)
	reload := time.NewTicker(quarantineReloadEvery)
	prune := time.NewTicker(quarantinePruneEvery)
	defer reload.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-reload.C:
			q.reloadLogged(ctx)
		case <-prune.C:
			q.pruneLogged(ctx)
		}
	}
}

func (q *Quarantine) reloadLogged(ctx context.Context) {
	if err := q.Reload(ctx); err != nil && q.log != nil {
		q.log.Warn("quarantine_reload_failed", "error", err.Error())
	}
}

func (q *Quarantine) pruneLogged(ctx context.Context) {
	removed, err := q.Prune(ctx)
	if q.log == nil {
		return
	}
	if err != nil {
		q.log.Warn("quarantine_prune_failed", "error", err.Error())
		return
	}
	if removed > 0 {
		q.log.Info("quarantine_pruned", "removed", removed)
	}
}
