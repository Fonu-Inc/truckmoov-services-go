package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type routingRepository struct {
	data map[string]domain.Job
}

func newRoutingRepository(jobs ...domain.Job) *routingRepository {
	r := &routingRepository{data: make(map[string]domain.Job)}
	for _, j := range jobs {
		r.data[j.TenantID+":"+j.ID] = j
	}
	return r
}

func (r *routingRepository) Save(job domain.Job) error {
	r.data[job.TenantID+":"+job.ID] = job
	return nil
}

func (r *routingRepository) FindByTenantAndID(tenantID, id string) (domain.Job, bool) {
	j, ok := r.data[tenantID+":"+id]
	return j, ok
}

func (r *routingRepository) FindByTenant(tenantID string) []domain.Job {
	var out []domain.Job
	for _, j := range r.data {
		if j.TenantID == tenantID {
			out = append(out, j)
		}
	}
	return out
}

func lat(v float64) *float64 { return &v }

func makeJobWithCoords(id, tenant string) domain.Job {
	return domain.Job{
		ID:       id,
		TenantID: tenant,
		Priority: domain.PriorityNormal,
		Pickup:   domain.JobStop{Address: "Apapa", Latitude: lat(6.45), Longitude: lat(3.35)},
		Dropoff:  domain.JobStop{Address: "Lekki", Latitude: lat(6.44), Longitude: lat(3.48)},
	}
}

func buildRoutingUseCase(jobs ...domain.Job) *RoutingUseCase {
	return NewRoutingUseCase(
		newRoutingRepository(jobs...),
		lifecycleClock{now: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)},
		"default",
	)
}

func TestRoutingUseCase_OptimizeRoute_SingleJobHappyPath(t *testing.T) {
	job := makeJobWithCoords("job-1", "tenant-a")
	uc := buildRoutingUseCase(job)

	plan, err := uc.OptimizeRoute(context.Background(), OptimizeRouteCommand{
		TenantID: "tenant-a",
		JobIDs:   []string{"job-1"},
		Origin:   RouteLocation{Latitude: 6.50, Longitude: 3.30},
		Vehicle:  RouteVehicleProfile{VehicleID: "v1", AverageSpeedKPH: 50},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.Feasible {
		t.Fatalf("expected feasible plan")
	}
	if len(plan.OrderedSteps) != 2 {
		t.Fatalf("expected 2 steps (pickup+dropoff), got %d", len(plan.OrderedSteps))
	}
	if plan.OrderedSteps[0].StopType != "pickup" {
		t.Fatalf("first step should be pickup")
	}
	if plan.TotalDistanceKm <= 0 {
		t.Fatalf("expected positive distance")
	}
}

func TestRoutingUseCase_OptimizeRoute_JobNotFound(t *testing.T) {
	uc := buildRoutingUseCase()
	_, err := uc.OptimizeRoute(context.Background(), OptimizeRouteCommand{
		TenantID: "tenant-a",
		JobIDs:   []string{"missing"},
		Origin:   RouteLocation{Latitude: 6.5, Longitude: 3.3},
	})
	if err == nil {
		t.Fatalf("expected error for missing job")
	}
}

func TestRoutingUseCase_OptimizeRoute_MissingOriginCoords(t *testing.T) {
	job := makeJobWithCoords("job-1", "default")
	uc := buildRoutingUseCase(job)
	_, err := uc.OptimizeRoute(context.Background(), OptimizeRouteCommand{
		JobIDs: []string{"job-1"},
		Origin: RouteLocation{},
	})
	if err == nil {
		t.Fatalf("expected error for missing origin coordinates")
	}
}

func TestRoutingUseCase_OptimizeRoute_MultipleJobs(t *testing.T) {
	j1 := makeJobWithCoords("job-1", "tenant-b")
	j2 := domain.Job{
		ID:       "job-2",
		TenantID: "tenant-b",
		Priority: domain.PriorityHigh,
		Pickup:   domain.JobStop{Address: "Ikeja", Latitude: lat(6.60), Longitude: lat(3.35)},
		Dropoff:  domain.JobStop{Address: "Yaba", Latitude: lat(6.52), Longitude: lat(3.38)},
	}
	uc := buildRoutingUseCase(j1, j2)

	plan, err := uc.OptimizeRoute(context.Background(), OptimizeRouteCommand{
		TenantID: "tenant-b",
		JobIDs:   []string{"job-1", "job-2"},
		Origin:   RouteLocation{Latitude: 6.55, Longitude: 3.33},
		Vehicle:  RouteVehicleProfile{AverageSpeedKPH: 40},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.OrderedSteps) != 4 {
		t.Fatalf("expected 4 steps for 2 jobs, got %d", len(plan.OrderedSteps))
	}
}

func TestRoutingUseCase_OptimizeRoute_EmptyJobIDs(t *testing.T) {
	uc := buildRoutingUseCase()
	_, err := uc.OptimizeRoute(context.Background(), OptimizeRouteCommand{
		TenantID: "tenant-a",
		JobIDs:   []string{},
		Origin:   RouteLocation{Latitude: 6.5, Longitude: 3.3},
	})
	if err == nil {
		t.Fatalf("expected error for empty job ids")
	}
}

func TestRoutingUseCase_InfeasibilityReasons_VehicleTypeMismatch(t *testing.T) {
	job := domain.Job{
		ID:                  "job-3",
		TenantID:            "tenant-c",
		Priority:            domain.PriorityNormal,
		RequiredVehicleType: "refrigerated",
		Pickup:              domain.JobStop{Address: "A", Latitude: lat(6.45), Longitude: lat(3.35)},
		Dropoff:             domain.JobStop{Address: "B", Latitude: lat(6.44), Longitude: lat(3.48)},
	}
	uc := buildRoutingUseCase(job)

	plan, err := uc.OptimizeRoute(context.Background(), OptimizeRouteCommand{
		TenantID: "tenant-c",
		JobIDs:   []string{"job-3"},
		Origin:   RouteLocation{Latitude: 6.50, Longitude: 3.30},
		Vehicle:  RouteVehicleProfile{VehicleType: "flatbed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Feasible {
		t.Fatalf("expected infeasible plan due to vehicle type mismatch")
	}
	if len(plan.InfeasibilityReasons) == 0 {
		t.Fatalf("expected infeasibility reasons")
	}
}

func TestRoutingUseCase_ShiftConstraint_ExceedsShiftEnd(t *testing.T) {
	job := makeJobWithCoords("job-4", "tenant-d")
	uc := buildRoutingUseCase(job)

	shiftEnd := time.Date(2026, 3, 1, 8, 1, 0, 0, time.UTC) // only 1 minute
	plan, err := uc.OptimizeRoute(context.Background(), OptimizeRouteCommand{
		TenantID:    "tenant-d",
		JobIDs:      []string{"job-4"},
		Origin:      RouteLocation{Latitude: 6.50, Longitude: 3.30},
		Vehicle:     RouteVehicleProfile{AverageSpeedKPH: 1}, // very slow
		DriverShift: DriverShift{End: &shiftEnd},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Feasible {
		t.Fatalf("expected infeasible due to shift end exceeded")
	}
}

func TestHaversineKm_KnownDistance(t *testing.T) {
	// Lagos to Abuja roughly 480km
	dist := haversineKm(6.45, 3.35, 9.07, 7.40)
	if dist < 400 || dist > 550 {
		t.Fatalf("unexpected haversine distance %.2f km", dist)
	}
}

func TestEstimateTravelSeconds_DefaultSpeed(t *testing.T) {
	secs := estimateTravelSeconds(35.0, 0) // 0 speed → use default 35kph
	if secs != 3600 {
		t.Fatalf("expected 3600s for 35km at default 35kph, got %d", secs)
	}
}
