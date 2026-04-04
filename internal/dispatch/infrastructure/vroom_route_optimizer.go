package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type VroomRouteOptimizer struct {
	client        *VroomClient
	repository    application.JobRepository
	clock         application.Clock
	defaultTenant string
}

func NewVroomRouteOptimizer(
	client *VroomClient,
	repository application.JobRepository,
	clock application.Clock,
	defaultTenant string,
) *VroomRouteOptimizer {
	return &VroomRouteOptimizer{
		client:        client,
		repository:    repository,
		clock:         clock,
		defaultTenant: defaultTenant,
	}
}

func (v *VroomRouteOptimizer) OptimizeRoute(ctx context.Context, cmd application.OptimizeRouteCommand) (application.RoutePlan, error) {
	if len(cmd.JobIDs) == 0 {
		return application.RoutePlan{}, errors.New("at least one job_id is required")
	}

	resolvedTenant := strings.TrimSpace(cmd.TenantID)
	if resolvedTenant == "" {
		resolvedTenant = v.defaultTenant
	}

	jobs := make([]domain.Job, 0, len(cmd.JobIDs))
	for _, jobID := range cmd.JobIDs {
		job, ok := v.repository.FindByTenantAndID(resolvedTenant, strings.TrimSpace(jobID))
		if !ok {
			return application.RoutePlan{}, application.ErrJobNotFound
		}
		jobs = append(jobs, job)
	}

	departure := v.resolveDepartureTime(cmd.DepartureTime, cmd.DriverShift)
	req, jobIndex, err := toVroomRequest(jobs, cmd)
	if err != nil {
		return application.RoutePlan{}, err
	}

	resp, err := v.client.Optimize(ctx, req)
	if err != nil {
		return application.RoutePlan{}, err
	}

	return toRoutePlan(resp, jobIndex, cmd, departure), nil
}

func toVroomRequest(jobs []domain.Job, cmd application.OptimizeRouteCommand) (VroomRequest, map[int]domain.Job, error) {
	if cmd.Origin.Latitude == 0 && cmd.Origin.Longitude == 0 {
		return VroomRequest{}, nil, errors.New("origin coordinates are required")
	}

	jobIndex := make(map[int]domain.Job, len(jobs))
	shipments := make([]Shipment, 0, len(jobs))
	for i, job := range jobs {
		if job.Pickup.Latitude == nil || job.Pickup.Longitude == nil {
			return VroomRequest{}, nil, fmt.Errorf("job %s pickup: coordinates are required", job.ID)
		}
		if job.Dropoff.Latitude == nil || job.Dropoff.Longitude == nil {
			return VroomRequest{}, nil, fmt.Errorf("job %s dropoff: coordinates are required", job.ID)
		}

		shipmentID := i + 1
		jobIndex[shipmentID] = job
		serviceSeconds := job.EstimatedServiceMinutes * 60
		shipments = append(shipments, Shipment{
			ID: shipmentID,
			Pickup: StopTask{
				Location: []float64{*job.Pickup.Longitude, *job.Pickup.Latitude},
				Service:  serviceSeconds,
			},
			Delivery: StopTask{
				Location: []float64{*job.Dropoff.Longitude, *job.Dropoff.Latitude},
				Service:  serviceSeconds,
			},
		})
	}

	return VroomRequest{
		Vehicles: []Vehicle{
			{
				ID:      1,
				Start:   []float64{cmd.Origin.Longitude, cmd.Origin.Latitude},
				Profile: resolveVehicleProfile(cmd.Vehicle.VehicleType),
			},
		},
		Shipments: shipments,
	}, jobIndex, nil
}

func toRoutePlan(
	resp VroomResponse,
	jobIndex map[int]domain.Job,
	cmd application.OptimizeRouteCommand,
	departure time.Time,
) application.RoutePlan {
	plan := application.RoutePlan{
		Feasible:             len(resp.Unassigned) == 0,
		VehicleID:            strings.TrimSpace(cmd.Vehicle.VehicleID),
		VehicleType:          strings.TrimSpace(cmd.Vehicle.VehicleType),
		DepartureTime:        departure,
		CompletionTime:       departure.Add(time.Duration(resp.Summary.Duration) * time.Second),
		ETASeconds:           resp.Summary.Duration,
		TotalDistanceKm:      roundDistanceKm(resp.Summary.Distance),
		TotalDurationSeconds: resp.Summary.Duration,
	}

	if len(resp.Unassigned) > 0 {
		reasons := make([]string, 0, len(resp.Unassigned))
		for _, unassigned := range resp.Unassigned {
			if job, ok := jobIndex[unassigned.ID]; ok {
				reasons = append(reasons, fmt.Sprintf("job %s could not be assigned", job.ID))
				continue
			}
			reasons = append(reasons, fmt.Sprintf("shipment %d could not be assigned", unassigned.ID))
		}
		plan.InfeasibilityReasons = reasons
	}

	if len(resp.Routes) == 0 {
		plan.CompletionTime = departure
		return plan
	}

	route := resp.Routes[0]
	steps := make([]application.RouteStep, 0, len(route.Steps))
	for _, step := range route.Steps {
		stopType, ok := mapStepType(step.Type)
		if !ok {
			continue
		}

		job, ok := jobIndex[step.Job]
		if !ok {
			continue
		}

		stop := job.Pickup
		label := "pickup"
		if stopType == "dropoff" {
			stop = job.Dropoff
			label = "dropoff"
		}

		latitude, longitude := 0.0, 0.0
		if len(step.Location) >= 2 {
			longitude = step.Location[0]
			latitude = step.Location[1]
		} else if stop.Latitude != nil && stop.Longitude != nil {
			latitude = *stop.Latitude
			longitude = *stop.Longitude
		}

		arrivalAt := departure.Add(time.Duration(step.Arrival) * time.Second)
		departureAt := departure.Add(time.Duration(step.Duration) * time.Second)
		steps = append(steps, application.RouteStep{
			Sequence:               len(steps) + 1,
			JobID:                  job.ID,
			StopType:               stopType,
			Address:                stop.Address,
			Label:                  label,
			Latitude:               latitude,
			Longitude:              longitude,
			DistanceFromPreviousKm: roundDistanceKm(step.Distance),
			TravelSecondsFromPrev:  maxInt64(step.Duration-step.Arrival, 0),
			ArrivalAt:              arrivalAt,
			DepartureAt:            departureAt,
			ETASeconds:             step.Arrival,
		})
	}
	plan.OrderedSteps = steps
	return plan
}

func (v *VroomRouteOptimizer) resolveDepartureTime(departureTime *time.Time, shift application.DriverShift) time.Time {
	if departureTime != nil && !departureTime.IsZero() {
		return departureTime.UTC()
	}
	departure := v.clock.Now().UTC()
	if shift.Start != nil && departure.Before(shift.Start.UTC()) {
		return shift.Start.UTC()
	}
	return departure
}

func resolveVehicleProfile(vehicleType string) string {
	switch strings.ToLower(strings.TrimSpace(vehicleType)) {
	case "bike", "motorbike":
		return "bike"
	default:
		return "car"
	}
}

func mapStepType(stepType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(stepType)) {
	case "pickup":
		return "pickup", true
	case "delivery":
		return "dropoff", true
	default:
		return "", false
	}
}

func roundDistanceKm(distanceMeters int64) float64 {
	return float64(distanceMeters) / 1000
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
