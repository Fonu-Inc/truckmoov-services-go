package application

import (
	"context"
	"testing"
	"time"
)

type fakeLatestReader struct {
	entries map[string][]LatestLocation
}

func newFakeLatestReader() *fakeLatestReader {
	return &fakeLatestReader{entries: make(map[string][]LatestLocation)}
}

func (r *fakeLatestReader) Save(loc LatestLocation) error {
	r.entries[loc.TenantID] = append(r.entries[loc.TenantID], loc)
	return nil
}

func (r *fakeLatestReader) Get(tenantID, vehicleID string) (*LatestLocation, error) {
	for _, e := range r.entries[tenantID] {
		if e.VehicleID == vehicleID {
			return &e, nil
		}
	}
	return nil, nil
}

func (r *fakeLatestReader) List(tenantID string) ([]LatestLocation, error) {
	return append([]LatestLocation{}, r.entries[tenantID]...), nil
}

type fakeHistoryReader struct {
	entries map[string][]LatestLocation
}

func newFakeHistoryReader() *fakeHistoryReader {
	return &fakeHistoryReader{entries: make(map[string][]LatestLocation)}
}

func (r *fakeHistoryReader) Append(e LatestLocation) error {
	key := e.TenantID + ":" + e.VehicleID
	r.entries[key] = append(r.entries[key], e)
	return nil
}

func (r *fakeHistoryReader) History(tenantID, vehicleID string, limit int) ([]LatestLocation, error) {
	key := tenantID + ":" + vehicleID
	list := r.entries[key]
	if limit > 0 && limit < len(list) {
		list = list[:limit]
	}
	return append([]LatestLocation{}, list...), nil
}

func buildQueryUseCase() (*TrackingQueryUseCase, *fakeLatestReader, *fakeHistoryReader) {
	lr := newFakeLatestReader()
	hr := newFakeHistoryReader()
	uc := NewTrackingQueryUseCase(lr, hr, "default")
	return uc, lr, hr
}

func TestTrackingQueryUseCase_ListLatestLocations_Empty(t *testing.T) {
	uc, _, _ := buildQueryUseCase()
	locs, err := uc.ListLatestLocations(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(locs) != 0 {
		t.Fatalf("expected empty list")
	}
}

func TestTrackingQueryUseCase_ListLatestLocations_ReturnsSavedEntries(t *testing.T) {
	uc, lr, _ := buildQueryUseCase()
	_ = lr.Save(LatestLocation{TenantID: "tenant-1", VehicleID: "v1"})
	_ = lr.Save(LatestLocation{TenantID: "tenant-1", VehicleID: "v2"})

	locs, err := uc.ListLatestLocations(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(locs))
	}
}

func TestTrackingQueryUseCase_ListLatestLocations_TenantIsolation(t *testing.T) {
	uc, lr, _ := buildQueryUseCase()
	_ = lr.Save(LatestLocation{TenantID: "tenant-a", VehicleID: "v1"})
	_ = lr.Save(LatestLocation{TenantID: "tenant-b", VehicleID: "v2"})

	locs, _ := uc.ListLatestLocations(context.Background(), "tenant-a")
	if len(locs) != 1 || locs[0].VehicleID != "v1" {
		t.Fatalf("expected only tenant-a vehicle, got %+v", locs)
	}
}

func TestTrackingQueryUseCase_ListLatestLocations_UsesDefaultTenant(t *testing.T) {
	uc, lr, _ := buildQueryUseCase()
	_ = lr.Save(LatestLocation{TenantID: "default", VehicleID: "v-default"})

	locs, _ := uc.ListLatestLocations(context.Background(), "")
	if len(locs) != 1 || locs[0].VehicleID != "v-default" {
		t.Fatalf("expected default tenant entry, got %+v", locs)
	}
}

func TestTrackingQueryUseCase_VehicleHistory_ReturnsHistory(t *testing.T) {
	uc, _, hr := buildQueryUseCase()
	now := time.Now()
	_ = hr.Append(LatestLocation{TenantID: "tenant-1", VehicleID: "v1", ObservedAt: now.Add(-2 * time.Minute)})
	_ = hr.Append(LatestLocation{TenantID: "tenant-1", VehicleID: "v1", ObservedAt: now.Add(-1 * time.Minute)})

	hist, err := uc.VehicleHistory(context.Background(), "tenant-1", "v1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(hist))
	}
}

func TestTrackingQueryUseCase_VehicleHistory_RespectsLimit(t *testing.T) {
	uc, _, hr := buildQueryUseCase()
	for i := 0; i < 5; i++ {
		_ = hr.Append(LatestLocation{TenantID: "tenant-1", VehicleID: "v2"})
	}

	hist, _ := uc.VehicleHistory(context.Background(), "tenant-1", "v2", 2)
	if len(hist) != 2 {
		t.Fatalf("expected 2 entries with limit=2, got %d", len(hist))
	}
}

func TestTrackingQueryUseCase_VehicleHistory_DefaultLimit(t *testing.T) {
	uc, _, hr := buildQueryUseCase()
	for i := 0; i < 10; i++ {
		_ = hr.Append(LatestLocation{TenantID: "tenant-1", VehicleID: "v3"})
	}

	hist, _ := uc.VehicleHistory(context.Background(), "tenant-1", "v3", 0)
	if len(hist) != 10 {
		t.Fatalf("expected 10 entries with limit=0 (no limit), got %d", len(hist))
	}
}

func TestTrackingQueryUseCase_VehicleHistory_EmptyVehicleID(t *testing.T) {
	uc, _, _ := buildQueryUseCase()
	hist, err := uc.VehicleHistory(context.Background(), "tenant-1", "  ", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hist == nil || len(hist) != 0 {
		t.Fatalf("expected empty non-nil history for blank vehicleID, got %#v", hist)
	}
}

type nilHistoryReader struct{ *fakeHistoryReader }

func (nilHistoryReader) History(string, string, int) ([]LatestLocation, error) { return nil, nil }

func TestTrackingQueryUseCase_VehicleHistory_EmptyIsNotNil(t *testing.T) {
	uc := NewTrackingQueryUseCase(newFakeLatestReader(), nilHistoryReader{newFakeHistoryReader()}, "default")

	history, err := uc.VehicleHistory(context.Background(), "tenant-1", "v-none", 0)
	if err != nil || history == nil || len(history) != 0 {
		t.Fatalf("expected an empty non-nil history, got %#v err=%v", history, err)
	}
}
