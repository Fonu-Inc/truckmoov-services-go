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
	readThroughTimeout = 2 * time.Second
	readThroughBackoff = 30 * time.Second
	negativeCacheFor   = 60 * time.Second
	negativeCacheMax   = 50000
	resultHit          = "hit"
	resultMiss         = "miss"
	resultNegative     = "negative_cached"
	resultError        = "error"
	resultBreakerOpen  = "breaker_open"
)

var ErrIdentitySourceDown = errors.New("fleet identity lookup unavailable")

// ReadThroughResolver answers from the cache and, on a miss, asks fleet about
// that one IMEI before anyone concludes the device is unknown. A fleet failure
// is an error, never an "unknown", so the sender retries instead of being
// quarantined.
type ReadThroughResolver struct {
	cache  IMEIResolver
	source IdentitySource
	seeder identitySeeder
	clock  Clock
	log    *slog.Logger

	mu           sync.Mutex
	negative     map[string]time.Time
	breakerUntil time.Time
}

func NewReadThroughResolver(cache IMEIResolver, source IdentitySource, gate *IngestGate, clock Clock, metrics IdentityMetrics, log *slog.Logger) *ReadThroughResolver {
	if metrics == nil {
		metrics = noopIdentityMetrics{}
	}
	return &ReadThroughResolver{
		cache:    cache,
		source:   source,
		seeder:   identitySeeder{cache: cache, gate: gate, metrics: metrics, log: log},
		clock:    clock,
		log:      log,
		negative: map[string]time.Time{},
	}
}

func (r *ReadThroughResolver) Resolve(imei string) (*DeviceIdentity, error) {
	imei = strings.TrimSpace(imei)
	identity, err := r.cache.Resolve(imei)
	if err != nil || identity != nil {
		return identity, err
	}
	return r.lookup(imei)
}

func (r *ReadThroughResolver) Upsert(identity DeviceIdentity) error {
	r.forget(strings.TrimSpace(identity.IMEI))
	return r.cache.Upsert(identity)
}

func (r *ReadThroughResolver) Evict(imei string) error {
	return r.cache.Evict(imei)
}

// EventSide is the view event consumers use: it reads the cache only, so a new
// device's own event never triggers a fleet call, yet its upsert still clears
// any "unknown" memory of that IMEI.
func (r *ReadThroughResolver) EventSide() IMEIResolver {
	return eventSideResolver{cache: r.cache, owner: r}
}

type eventSideResolver struct {
	cache IMEIResolver
	owner *ReadThroughResolver
}

func (e eventSideResolver) Resolve(imei string) (*DeviceIdentity, error) {
	return e.cache.Resolve(imei)
}
func (e eventSideResolver) Upsert(identity DeviceIdentity) error { return e.owner.Upsert(identity) }
func (e eventSideResolver) Evict(imei string) error              { return e.cache.Evict(imei) }

func (r *ReadThroughResolver) lookup(imei string) (*DeviceIdentity, error) {
	now := r.clock.Now()
	if r.knownUnknown(imei, now) {
		r.seeder.metrics.ReadThrough(resultNegative)
		return nil, nil
	}
	if r.breakerOpen(now) {
		r.seeder.metrics.ReadThrough(resultBreakerOpen)
		return nil, ErrIdentitySourceDown
	}
	ctx, cancel := context.WithTimeout(context.Background(), readThroughTimeout)
	defer cancel()
	found, err := r.source.Lookup(ctx, imei)
	if err != nil {
		r.tripBreaker(now)
		r.seeder.metrics.ReadThrough(resultError)
		r.log.Warn("identity_read_through_failed", "imei", imei, "error", err.Error())
		return nil, err
	}
	if found == nil {
		r.remember(imei, now)
		r.seeder.metrics.ReadThrough(resultMiss)
		return nil, nil
	}
	found.IMEI = imei
	if err := r.seeder.seed(*found); err != nil {
		return nil, err
	}
	r.seeder.metrics.ReadThrough(resultHit)
	return r.cache.Resolve(imei)
}

func (r *ReadThroughResolver) knownUnknown(imei string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	until, ok := r.negative[imei]
	if ok && now.Before(until) {
		return true
	}
	delete(r.negative, imei)
	return false
}

func (r *ReadThroughResolver) remember(imei string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.negative) >= negativeCacheMax {
		for key, until := range r.negative {
			if !now.Before(until) {
				delete(r.negative, key)
			}
		}
	}
	if len(r.negative) < negativeCacheMax {
		r.negative[imei] = now.Add(negativeCacheFor)
	}
}

func (r *ReadThroughResolver) forget(imei string) {
	r.mu.Lock()
	delete(r.negative, imei)
	r.mu.Unlock()
}

func (r *ReadThroughResolver) breakerOpen(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Before(r.breakerUntil)
}

func (r *ReadThroughResolver) tripBreaker(now time.Time) {
	r.mu.Lock()
	r.breakerUntil = now.Add(readThroughBackoff)
	r.mu.Unlock()
}
