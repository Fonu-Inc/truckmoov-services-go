package application

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	SkipTenantSuspended = "tenant_suspended"
	SkipDeviceSuspended = "device_suspended"
	SkipUnknownDevice   = "unknown_device"

	blockedLogEvery = 5 * time.Minute
	seenWriteEvery  = time.Minute
)

type SuspensionSnapshot struct {
	Tenants map[string]struct{}
	IMEIs   map[string]struct{}
}

type GateMetrics interface {
	Blocked(reason string)
	ReloadFailed()
}

type SuspendedSeenStore interface {
	RecordSeen(id string, at time.Time) error
}

type tenantMark struct {
	tenantID  string
	suspended bool
	at        time.Time
}

type IngestGate struct {
	seen    SuspendedSeenStore
	metrics GateMetrics
	clock   Clock
	log     *slog.Logger

	mu      sync.RWMutex
	tenants map[string]struct{}
	imeis   map[string]struct{}
	marks   []tenantMark
	loaded  bool

	stats     *gateStats
	logLimit  *rateLimiter
	seenLimit *rateLimiter
}

func NewIngestGate(seen SuspendedSeenStore, metrics GateMetrics, clock Clock, log *slog.Logger) *IngestGate {
	return &IngestGate{
		seen:      seen,
		metrics:   metrics,
		clock:     clock,
		log:       log,
		tenants:   map[string]struct{}{},
		imeis:     map[string]struct{}{},
		stats:     newGateStats(clock.Now()),
		logLimit:  newRateLimiter(blockedLogEvery),
		seenLimit: newRateLimiter(seenWriteEvery),
	}
}

func (g *IngestGate) Ready() bool {
	if g == nil {
		return true
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.loaded
}

func (g *IngestGate) MarkTenant(tenantID string, suspended bool) {
	tenantID = strings.TrimSpace(tenantID)
	if g == nil || tenantID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.applyTenant(tenantID, suspended)
	g.marks = append(g.marks, tenantMark{tenantID: tenantID, suspended: suspended, at: g.clock.Now()})
}

func (g *IngestGate) applyTenant(tenantID string, suspended bool) {
	if suspended {
		g.tenants[tenantID] = struct{}{}
		return
	}
	delete(g.tenants, tenantID)
}

func (g *IngestGate) Begin() time.Time {
	return g.clock.Now()
}

// Replace swaps in a snapshot taken after startedAt and re-applies tenant events
// that arrived while it was loading, so a slow load cannot undo a newer event.
func (g *IngestGate) Replace(snapshot SuspensionSnapshot, startedAt time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tenants = copySet(snapshot.Tenants)
	g.imeis = copySet(snapshot.IMEIs)
	kept := g.marks[:0]
	for _, mark := range g.marks {
		if mark.at.Before(startedAt) {
			continue
		}
		g.applyTenant(mark.tenantID, mark.suspended)
		kept = append(kept, mark)
	}
	g.marks = kept
	g.loaded = true
}

// DeviceSuspended reports whether the last loaded list holds this IMEI.
func (g *IngestGate) DeviceSuspended(imei string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, held := g.imeis[strings.TrimSpace(imei)]
	return held
}

func (g *IngestGate) ReloadFailed() {
	if g != nil && g.metrics != nil {
		g.metrics.ReloadFailed()
	}
}

// Evaluate returns the skip reason, or "" when the message may pass.
func (g *IngestGate) Evaluate(tenantID string, identity *DeviceIdentity, imei string) string {
	if g == nil {
		return ""
	}
	imei = strings.TrimSpace(imei)
	reason := g.decide(tenantID, identity, imei)
	if reason != "" {
		g.record(reason, tenantID, identity, imei)
	}
	return reason
}

func (g *IngestGate) decide(tenantID string, identity *DeviceIdentity, imei string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, blocked := g.tenants[tenantID]; blocked {
		return SkipTenantSuspended
	}
	if imei == "" {
		return ""
	}
	if identity != nil {
		if identity.Suspended {
			return SkipDeviceSuspended
		}
		return ""
	}
	if _, blocked := g.imeis[imei]; blocked {
		return SkipDeviceSuspended
	}
	return ""
}

func (g *IngestGate) record(reason, tenantID string, identity *DeviceIdentity, imei string) {
	if g.metrics != nil {
		g.metrics.Blocked(reason)
	}
	now := g.clock.Now()
	key := imei
	if key == "" {
		key = "tenant:" + tenantID
	}
	if reason != SkipUnknownDevice {
		g.stats.add(key, blockedDevice(reason, tenantID, identity, imei), now)
	}
	if g.log != nil && g.logLimit.allow(key, now) {
		g.log.Warn("ingest_blocked", "reason", reason, "tenant", tenantID, "imei", imei)
	}
	if imei == "" || reason == SkipUnknownDevice || g.seen == nil || !g.seenLimit.allow(imei, now) {
		return
	}
	if err := g.seen.RecordSeen(imei, now); err != nil && g.log != nil {
		g.log.Warn("suspended_seen_write_failed", "imei", imei, "error", err.Error())
	}
}

func copySet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

type rateLimiter struct {
	every time.Duration
	mu    sync.Mutex
	last  map[string]time.Time
}

func newRateLimiter(every time.Duration) *rateLimiter {
	return &rateLimiter{every: every, last: map[string]time.Time{}}
}

func (r *rateLimiter) allow(key string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if at, ok := r.last[key]; ok && now.Sub(at) < r.every {
		return false
	}
	if len(r.last) >= 50000 {
		r.prune(now)
	}
	r.last[key] = now
	return true
}

func (r *rateLimiter) prune(now time.Time) {
	for key, at := range r.last {
		if now.Sub(at) >= r.every {
			delete(r.last, key)
		}
	}
}

func blockedDevice(reason, tenantID string, identity *DeviceIdentity, imei string) BlockedDevice {
	device := BlockedDevice{IMEI: imei, TenantID: tenantID, BlockedBy: blockedBy(reason)}
	if identity != nil {
		device.DeviceID = identity.DeviceID
		device.TenantID = firstNonEmpty(identity.TenantID, tenantID)
	}
	return device
}
