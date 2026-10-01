package application

import (
	"sort"
	"sync"
	"time"
)

const (
	BlockedByTenant = "tenant"
	BlockedByDevice = "device"

	CountsSinceRestart = "service_restart"
	gateStatsMaxKeys   = 20000
)

// BlockedDevice is what the gate has seen from one blocked sender. Counts live
// in memory only, so they restart from zero with the service.
type BlockedDevice struct {
	DeviceID     string    `json:"deviceId"`
	IMEI         string    `json:"imei"`
	TenantID     string    `json:"tenantId"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
	DroppedToday int       `json:"droppedToday"`
	DroppedTotal int       `json:"droppedTotal"`
	BlockedBy    string    `json:"blockedReason"`
}

type gateStats struct {
	mu      sync.Mutex
	since   time.Time
	entries map[string]*statEntry
}

type statEntry struct {
	device BlockedDevice
	day    string
}

func newGateStats(now time.Time) *gateStats {
	return &gateStats{since: now.UTC(), entries: map[string]*statEntry{}}
}

func (s *gateStats) add(key string, device BlockedDevice, now time.Time) {
	now = now.UTC()
	day := now.Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		if len(s.entries) >= gateStatsMaxKeys {
			return
		}
		entry = &statEntry{}
		s.entries[key] = entry
	}
	if entry.day != day {
		entry.day = day
		entry.device.DroppedToday = 0
	}
	entry.device.DeviceID = firstNonEmpty(device.DeviceID, entry.device.DeviceID)
	entry.device.IMEI = device.IMEI
	entry.device.TenantID = firstNonEmpty(device.TenantID, entry.device.TenantID)
	entry.device.BlockedBy = device.BlockedBy
	entry.device.LastSeenAt = now
	entry.device.DroppedToday++
	entry.device.DroppedTotal++
}

func (s *gateStats) snapshot(now time.Time) []BlockedDevice {
	day := now.UTC().Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BlockedDevice, 0, len(s.entries))
	for _, entry := range s.entries {
		device := entry.device
		if entry.day != day {
			device.DroppedToday = 0
		}
		out = append(out, device)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeenAt.After(out[j].LastSeenAt) })
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type BlockedDevicesReport struct {
	CountsSince string          `json:"countsSince"`
	StartedAt   time.Time       `json:"startedAt"`
	Devices     []BlockedDevice `json:"devices"`
}

// BlockedDevices lists senders the gate dropped since the service started.
func (g *IngestGate) BlockedDevices() BlockedDevicesReport {
	report := BlockedDevicesReport{CountsSince: CountsSinceRestart, Devices: []BlockedDevice{}}
	if g == nil {
		return report
	}
	report.StartedAt = g.stats.since
	report.Devices = g.stats.snapshot(g.clock.Now())
	return report
}

func blockedBy(reason string) string {
	if reason == SkipTenantSuspended {
		return BlockedByTenant
	}
	return BlockedByDevice
}
