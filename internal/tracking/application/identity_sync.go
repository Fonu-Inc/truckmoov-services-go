package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

const (
	identityPageSize       = 1000
	identityMaxPages       = 2000
	identityJitterFraction = 10
)

var ErrWarmingUp = errors.New("identity cache warming up")

type IdentityPage struct {
	Identities []DeviceIdentity
	NextCursor string
}

// IdentitySource is fleet's record of every live device. Lookup answers
// (nil, nil) only for a definitive "no such device"; any failure is an error.
type IdentitySource interface {
	Page(ctx context.Context, cursor string, limit int) (IdentityPage, error)
	Lookup(ctx context.Context, imei string) (*DeviceIdentity, error)
}

type IdentityMetrics interface {
	SnapshotSize(n int)
	ReadThrough(result string)
	Conflict()
	Quarantined()
}

type noopIdentityMetrics struct{}

func (noopIdentityMetrics) SnapshotSize(int)   {}
func (noopIdentityMetrics) ReadThrough(string) {}
func (noopIdentityMetrics) Conflict()          {}
func (noopIdentityMetrics) Quarantined()       {}

// Warmup holds ingest back until the first identity snapshot is in, but never
// past the deadline: after that ingest relies on read-through alone.
type Warmup struct {
	clock    Clock
	deadline time.Time
	mu       sync.RWMutex
	done     bool
}

func NewWarmup(clock Clock, timeout time.Duration) *Warmup {
	return &Warmup{clock: clock, deadline: clock.Now().Add(timeout)}
}

func (w *Warmup) Done() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.done = true
	w.mu.Unlock()
}

func (w *Warmup) Ready() bool {
	if w == nil {
		return true
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.done || !w.clock.Now().Before(w.deadline)
}

// identitySeeder writes fleet's identities into the cache. The first tenant to
// hold an IMEI keeps it, and a held device keeps its suspended flag.
type identitySeeder struct {
	cache   IMEIResolver
	gate    *IngestGate
	metrics IdentityMetrics
	log     *slog.Logger
}

func (s identitySeeder) seed(identity DeviceIdentity) error {
	identity.IMEI = strings.TrimSpace(identity.IMEI)
	identity.TenantID = strings.TrimSpace(identity.TenantID)
	if identity.IMEI == "" || identity.TenantID == "" {
		return nil
	}
	existing, err := s.cache.Resolve(identity.IMEI)
	if err != nil {
		return err
	}
	if existing == nil {
		identity.Suspended = s.gate.DeviceSuspended(identity.IMEI)
		return s.cache.Upsert(identity)
	}
	if existing.TenantID != identity.TenantID {
		s.metrics.Conflict()
		s.log.Warn("device_identity_imei_conflict", "imei", identity.IMEI, "fleet_tenant", identity.TenantID, "cached_tenant", existing.TenantID)
		return nil
	}
	identity.Suspended = existing.Suspended
	if *existing == identity {
		return nil
	}
	return s.cache.Upsert(identity)
}

type IdentitySync struct {
	source IdentitySource
	seeder identitySeeder
	warmup *Warmup
	log    *slog.Logger
}

func NewIdentitySync(source IdentitySource, cache IMEIResolver, gate *IngestGate, warmup *Warmup, metrics IdentityMetrics, log *slog.Logger) *IdentitySync {
	if metrics == nil {
		metrics = noopIdentityMetrics{}
	}
	return &IdentitySync{
		source: source,
		seeder: identitySeeder{cache: cache, gate: gate, metrics: metrics, log: log},
		warmup: warmup,
		log:    log,
	}
}

func (s *IdentitySync) Reload(ctx context.Context) error {
	cursor, total := "", 0
	for pages := 0; pages < identityMaxPages; pages++ {
		page, err := s.source.Page(ctx, cursor, identityPageSize)
		if err != nil {
			return err
		}
		for _, identity := range page.Identities {
			if err := s.seeder.seed(identity); err != nil {
				return fmt.Errorf("seed %s: %w", identity.IMEI, err)
			}
		}
		total += len(page.Identities)
		if page.NextCursor == "" {
			s.seeder.metrics.SnapshotSize(total)
			s.warmup.Done()
			s.log.Info("identity_snapshot_loaded", "devices", total)
			return nil
		}
		cursor = page.NextCursor
	}
	return fmt.Errorf("more than %d pages", identityMaxPages)
}

// Run loads at once, retrying with backoff until it succeeds, then refreshes
// every interval plus jitter so pods do not hit fleet together.
func (s *IdentitySync) Run(ctx context.Context, interval time.Duration) {
	var wait time.Duration
	failures := 0
	for sleepCtx(ctx, wait) {
		if err := s.Reload(ctx); err != nil {
			failures++
			s.log.Warn("identity_snapshot_failed", "error", err.Error())
			wait = min(interval, time.Second<<min(failures, 6))
			continue
		}
		failures = 0
		wait = interval + rand.N(interval/identityJitterFraction+1)
	}
}
