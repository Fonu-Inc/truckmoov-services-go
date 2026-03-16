package infrastructure

import (
	"testing"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

func TestInMemoryLocationHistoryRepository_AppendAndHistory(t *testing.T) {
	repo := NewInMemoryLocationHistoryRepository()

	entries := []application.LatestLocation{
		{TenantID: "t1", VehicleID: "v1", Latitude: 6.1},
		{TenantID: "t1", VehicleID: "v1", Latitude: 6.2},
		{TenantID: "t1", VehicleID: "v1", Latitude: 6.3},
	}
	for _, e := range entries {
		if err := repo.Append(e); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	hist, err := repo.History("t1", "v1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("expected 3, got %d", len(hist))
	}
	// newest-first order
	if hist[0].Latitude != 6.3 {
		t.Fatalf("expected newest entry first, got lat=%v", hist[0].Latitude)
	}
}

func TestInMemoryLocationHistoryRepository_LimitRespected(t *testing.T) {
	repo := NewInMemoryLocationHistoryRepository()
	for i := 0; i < 10; i++ {
		_ = repo.Append(application.LatestLocation{TenantID: "t1", VehicleID: "v2"})
	}

	hist, _ := repo.History("t1", "v2", 3)
	if len(hist) != 3 {
		t.Fatalf("expected 3, got %d", len(hist))
	}
}

func TestInMemoryLocationHistoryRepository_ZeroLimitReturnsAll(t *testing.T) {
	repo := NewInMemoryLocationHistoryRepository()
	for i := 0; i < 5; i++ {
		_ = repo.Append(application.LatestLocation{TenantID: "t1", VehicleID: "v3"})
	}

	hist, _ := repo.History("t1", "v3", 0)
	if len(hist) != 5 {
		t.Fatalf("expected 5, got %d", len(hist))
	}
}

func TestInMemoryLocationHistoryRepository_TenantVehicleIsolation(t *testing.T) {
	repo := NewInMemoryLocationHistoryRepository()
	_ = repo.Append(application.LatestLocation{TenantID: "t1", VehicleID: "v1"})
	_ = repo.Append(application.LatestLocation{TenantID: "t2", VehicleID: "v1"})

	hist, _ := repo.History("t1", "v1", 10)
	if len(hist) != 1 {
		t.Fatalf("expected 1 entry for t1:v1, got %d", len(hist))
	}
}

func TestInMemoryLocationHistoryRepository_EmptyHistory(t *testing.T) {
	repo := NewInMemoryLocationHistoryRepository()
	hist, err := repo.History("none", "none", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hist != nil {
		t.Fatalf("expected nil for missing key, got %v", hist)
	}
}

func TestInMemoryLocationHistoryRepository_CapsAtMaxEntries(t *testing.T) {
	repo := NewInMemoryLocationHistoryRepository()
	for i := 0; i < maxHistoryEntries+10; i++ {
		_ = repo.Append(application.LatestLocation{TenantID: "t1", VehicleID: "v4"})
	}

	hist, _ := repo.History("t1", "v4", 0)
	if len(hist) != maxHistoryEntries {
		t.Fatalf("expected cap at %d, got %d", maxHistoryEntries, len(hist))
	}
}
