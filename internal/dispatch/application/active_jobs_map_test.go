package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type mapJobRepo struct {
	jobs map[string][]domain.Job
}

func (r *mapJobRepo) Save(job domain.Job) error { return nil }
func (r *mapJobRepo) FindByTenantAndID(tenantID, id string) (domain.Job, bool) {
	return domain.Job{}, false
}
func (r *mapJobRepo) FindByTenant(tenantID string) []domain.Job { return r.jobs[tenantID] }
func (r *mapJobRepo) Delete(tenantID, id string) error          { return nil }

type countingShaper struct {
	calls int
	err   error
}

func (s *countingShaper) RouteShape(_ context.Context, _ []RoutePoint) (string, string, error) {
	s.calls++
	if s.err != nil {
		return "", "", s.err
	}
	return "encoded-shape", "polyline6", nil
}

func coord(v float64) *float64 { return &v }

func jobWithStops(id, tenant string, status domain.JobStatus) domain.Job {
	return domain.Job{
		ID:       id,
		TenantID: tenant,
		Status:   status,
		Pickup:   domain.JobStop{Address: "Apapa Port", Latitude: coord(6.45), Longitude: coord(3.36)},
		Dropoff:  domain.JobStop{Address: "Sango", Latitude: coord(6.70), Longitude: coord(3.24)},
		Stops:    []domain.JobStop{{Address: "Mile 2", Latitude: coord(6.46), Longitude: coord(3.32)}},
	}
}

func TestActiveJobsMap_ReturnsOnlyActiveJobsWithCounts(t *testing.T) {
	repo := &mapJobRepo{jobs: map[string][]domain.Job{
		"tenant-a": {
			jobWithStops("j1", "tenant-a", domain.StatusEnRoute),
			jobWithStops("j2", "tenant-a", domain.StatusDelivered),
			jobWithStops("j3", "tenant-a", domain.StatusCreated),
			jobWithStops("j4", "tenant-a", domain.StatusAssigned),
		},
	}}
	uc := NewActiveJobsMapUseCase(repo, fixedClock{now: time.Now()}, "", time.Minute, &countingShaper{})

	result, err := uc.Execute(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("expected 2 active jobs, got %d", len(result.Jobs))
	}
	if result.Counts.ActiveDeliveries != 2 {
		t.Fatalf("expected 2 active deliveries, got %d", result.Counts.ActiveDeliveries)
	}
	// Three waypoints per job: pickup, one intermediate stop, dropoff.
	if result.Counts.Stops != 6 {
		t.Fatalf("expected 6 stops, got %d", result.Counts.Stops)
	}
	if result.Counts.Pickups != 2 || result.Counts.Dropoffs != 2 {
		t.Fatalf("expected 2 pickups and 2 dropoffs, got %d and %d", result.Counts.Pickups, result.Counts.Dropoffs)
	}
	if result.Jobs[0].RouteShape != "encoded-shape" || result.Jobs[0].RouteEncoding != "polyline6" {
		t.Fatalf("expected route geometry on the first job, got %q", result.Jobs[0].RouteShape)
	}
}

func TestActiveJobsMap_NeverReturnsAnotherTenantsJobs(t *testing.T) {
	repo := &mapJobRepo{jobs: map[string][]domain.Job{
		"tenant-a": {jobWithStops("j1", "tenant-a", domain.StatusEnRoute)},
		"tenant-b": {jobWithStops("j2", "tenant-b", domain.StatusEnRoute)},
	}}
	uc := NewActiveJobsMapUseCase(repo, fixedClock{now: time.Now()}, "", time.Minute, &countingShaper{})

	result, err := uc.Execute(context.Background(), "tenant-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Jobs) != 1 || result.Jobs[0].JobID != "j2" {
		t.Fatalf("tenant-b must see only its own job, got %+v", result.Jobs)
	}
}

func TestActiveJobsMap_CachesGeometryAcrossCalls(t *testing.T) {
	repo := &mapJobRepo{jobs: map[string][]domain.Job{
		"tenant-a": {jobWithStops("j1", "tenant-a", domain.StatusEnRoute)},
	}}
	shaper := &countingShaper{}
	uc := NewActiveJobsMapUseCase(repo, fixedClock{now: time.Now()}, "", time.Minute, shaper)

	for i := 0; i < 3; i++ {
		if _, err := uc.Execute(context.Background(), "tenant-a"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if shaper.calls != 1 {
		t.Fatalf("expected geometry to be computed once, got %d calls", shaper.calls)
	}
}

func TestActiveJobsMap_DegradesWhenShaperFails(t *testing.T) {
	repo := &mapJobRepo{jobs: map[string][]domain.Job{
		"tenant-a": {jobWithStops("j1", "tenant-a", domain.StatusEnRoute)},
	}}
	uc := NewActiveJobsMapUseCase(repo, fixedClock{now: time.Now()}, "", time.Minute, &countingShaper{err: errors.New("valhalla down")})

	result, err := uc.Execute(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("a routing outage must not fail the map: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("expected the job to still be plotted, got %d", len(result.Jobs))
	}
	if result.Jobs[0].RouteShape != "" {
		t.Fatalf("expected no geometry, got %q", result.Jobs[0].RouteShape)
	}
	if len(result.Jobs[0].Waypoints) != 3 {
		t.Fatalf("expected pins to survive, got %d waypoints", len(result.Jobs[0].Waypoints))
	}
}

func TestActiveJobsMap_SkipsGeometryWithoutEnoughCoordinates(t *testing.T) {
	job := jobWithStops("j1", "tenant-a", domain.StatusEnRoute)
	job.Stops = nil
	job.Dropoff = domain.JobStop{Address: "Unknown"}
	repo := &mapJobRepo{jobs: map[string][]domain.Job{"tenant-a": {job}}}
	shaper := &countingShaper{}
	uc := NewActiveJobsMapUseCase(repo, fixedClock{now: time.Now()}, "", time.Minute, shaper)

	result, err := uc.Execute(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shaper.calls != 0 {
		t.Fatalf("a single located point cannot be routed; expected no call, got %d", shaper.calls)
	}
	if len(result.Jobs[0].Waypoints) != 2 {
		t.Fatalf("expected pickup and dropoff waypoints, got %d", len(result.Jobs[0].Waypoints))
	}
}
