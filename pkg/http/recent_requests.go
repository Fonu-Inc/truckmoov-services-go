package httpx

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// RequestSnapshot is a compact record of one handled request, kept around so
// an error alert can show what the caller was doing right before it failed.
type RequestSnapshot struct {
	Method   string
	Path     string
	Status   int
	TenantID string
	At       time.Time
}

func (s RequestSnapshot) String() string {
	tenant := s.TenantID
	if tenant == "" {
		tenant = "-"
	}
	return fmt.Sprintf("%s  %s %s -> %d (tenant=%s)", s.At.Format(time.RFC3339), s.Method, s.Path, s.Status, tenant)
}

// RecentRequests is a fixed-size ring buffer of the most recent requests
// handled by a service process. It is process-wide, not per-tenant.
type RecentRequests struct {
	mu    sync.Mutex
	items []RequestSnapshot
	max   int
}

func NewRecentRequests(max int) *RecentRequests {
	if max <= 0 {
		max = 3
	}
	return &RecentRequests{max: max}
}

// Add records a snapshot, evicting the oldest entry once the buffer is full.
func (r *RecentRequests) Add(s RequestSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.items = append(r.items, s)
	if len(r.items) > r.max {
		r.items = r.items[len(r.items)-r.max:]
	}
}

// Snapshot returns the requests recorded so far, oldest first. Call it
// before adding the current request to see what happened right before it.
func (r *RecentRequests) Snapshot() []RequestSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]RequestSnapshot, len(r.items))
	copy(out, r.items)
	return out
}

func FormatRecentRequests(snapshots []RequestSnapshot) string {
	if len(snapshots) == 0 {
		return "(no prior requests recorded)"
	}
	lines := make([]string, len(snapshots))
	for i, s := range snapshots {
		lines[i] = s.String()
	}
	return strings.Join(lines, "\n")
}
