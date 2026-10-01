package application

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

type SuspensionSource interface {
	Load(ctx context.Context) (SuspensionSnapshot, error)
}

// SuspendedIMEIIndex is the shared record of which devices were suspended at the
// last load, so any pod can find identities that still carry a stale flag.
type SuspendedIMEIIndex interface {
	Members() ([]string, error)
	Add(imeis ...string) error
	Remove(imeis ...string) error
}

type SuspensionSync struct {
	source   SuspensionSource
	gate     *IngestGate
	resolver IMEIResolver
	index    SuspendedIMEIIndex
	log      *slog.Logger
}

func NewSuspensionSync(source SuspensionSource, gate *IngestGate, resolver IMEIResolver, index SuspendedIMEIIndex, log *slog.Logger) *SuspensionSync {
	return &SuspensionSync{source: source, gate: gate, resolver: resolver, index: index, log: log}
}

func (s *SuspensionSync) Reload(ctx context.Context) error {
	started := s.gate.Begin()
	snapshot, err := s.source.Load(ctx)
	if err != nil {
		s.gate.ReloadFailed()
		return err
	}
	s.gate.Replace(snapshot, started)
	s.reconcileIdentities(snapshot)
	return nil
}

// LoadInitial tries a few times so ingest starts with the lists in place.
func (s *SuspensionSync) LoadInitial(ctx context.Context, attempts int, backoff time.Duration) error {
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if err = s.Reload(ctx); err == nil {
			return nil
		}
		s.log.Warn("suspension_load_failed", "attempt", attempt+1, "error", err.Error())
		if !sleepCtx(ctx, backoff<<attempt) {
			return ctx.Err()
		}
	}
	return err
}

// Run reloads every interval until ctx ends; after a failure it retries sooner,
// and the last good lists stay in force.
func (s *SuspensionSync) Run(ctx context.Context, interval time.Duration) {
	wait := interval
	failures := 0
	for sleepCtx(ctx, wait) {
		if err := s.Reload(ctx); err != nil {
			failures++
			s.log.Warn("suspension_reload_failed", "error", err.Error())
			wait = min(interval, time.Second<<min(failures, 6))
			continue
		}
		failures = 0
		wait = interval
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *SuspensionSync) reconcileIdentities(snapshot SuspensionSnapshot) {
	previous, err := s.index.Members()
	if err != nil {
		s.log.Warn("suspended_index_read_failed", "error", err.Error())
		previous = nil
	}
	for imei := range snapshot.IMEIs {
		s.setIdentityFlag(imei, true)
	}
	var released []string
	for _, imei := range previous {
		if _, still := snapshot.IMEIs[imei]; still {
			continue
		}
		s.setIdentityFlag(imei, false)
		released = append(released, imei)
	}
	s.writeIndex(snapshot, released)
}

func (s *SuspensionSync) setIdentityFlag(imei string, suspended bool) {
	identity, err := s.resolver.Resolve(imei)
	if err != nil || identity == nil || identity.Suspended == suspended {
		return
	}
	identity.Suspended = suspended
	if err := s.resolver.Upsert(*identity); err != nil {
		s.log.Warn("identity_flag_update_failed", "imei", imei, "error", err.Error())
	}
}

func (s *SuspensionSync) writeIndex(snapshot SuspensionSnapshot, released []string) {
	if len(released) > 0 {
		if err := s.index.Remove(released...); err != nil {
			s.log.Warn("suspended_index_write_failed", "error", err.Error())
		}
	}
	current := make([]string, 0, len(snapshot.IMEIs))
	for imei := range snapshot.IMEIs {
		current = append(current, imei)
	}
	if len(current) == 0 {
		return
	}
	if err := s.index.Add(current...); err != nil {
		s.log.Warn("suspended_index_write_failed", "error", err.Error())
	}
}

// ConsumeTenantSuspensionUseCase applies tm.*.tenant.suspended and
// tm.*.tenant.reinstated to this pod's tenant set right away.
type ConsumeTenantSuspensionUseCase struct {
	gate *IngestGate
}

func NewConsumeTenantSuspensionUseCase(gate *IngestGate) *ConsumeTenantSuspensionUseCase {
	return &ConsumeTenantSuspensionUseCase{gate: gate}
}

func (u *ConsumeTenantSuspensionUseCase) HandleSuspended(_ context.Context, envelope eventbus.EventEnvelope) error {
	u.gate.MarkTenant(eventTenantID(envelope), true)
	return nil
}

func (u *ConsumeTenantSuspensionUseCase) HandleReinstated(_ context.Context, envelope eventbus.EventEnvelope) error {
	u.gate.MarkTenant(eventTenantID(envelope), false)
	return nil
}

func eventTenantID(envelope eventbus.EventEnvelope) string {
	return strings.TrimSpace(envelope.TenantID)
}
