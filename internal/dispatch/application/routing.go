package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const defaultAverageSpeedKPH = 35.0

type RouteLocation struct {
	Label     string  `json:"label,omitempty"`
	Address   string  `json:"address,omitempty"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type RouteVehicleProfile struct {
	VehicleID        string  `json:"vehicle_id,omitempty"`
	VehicleType      string  `json:"vehicle_type,omitempty"`
	CapacityWeightKg float64 `json:"capacity_weight_kg,omitempty"`
	CapacityVolumeM3 float64 `json:"capacity_volume_m3,omitempty"`
	AverageSpeedKPH  float64 `json:"average_speed_kph,omitempty"`
}

type DriverShift struct {
	Start *time.Time `json:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"`
}

type RouteStep struct {
	Sequence               int             `json:"sequence"`
	JobID                  string          `json:"job_id"`
	StopType               string          `json:"stop_type"`
	Address                string          `json:"address"`
	Label                  string          `json:"label,omitempty"`
	Latitude               float64         `json:"latitude"`
	Longitude              float64         `json:"longitude"`
	DistanceFromPreviousKm float64         `json:"distance_from_previous_km"`
	TravelSecondsFromPrev  int64           `json:"travel_seconds_from_prev"`
	ArrivalAt              time.Time       `json:"arrival_at"`
	DepartureAt            time.Time       `json:"departure_at"`
	ETASeconds             int64           `json:"eta_seconds"`
	Shape                  string          `json:"shape,omitempty"`
	AlternateShapes        []string        `json:"alternate_shapes,omitempty"`
	Maneuvers              []RouteManeuver `json:"maneuvers,omitempty"`
}

type RouteManeuver struct {
	Type                             int      `json:"type"`
	Instruction                      string   `json:"instruction,omitempty"`
	VerbalTransitionAlertInstruction string   `json:"verbal_transition_alert_instruction,omitempty"`
	VerbalPreTransitionInstruction   string   `json:"verbal_pre_transition_instruction,omitempty"`
	VerbalPostTransitionInstruction  string   `json:"verbal_post_transition_instruction,omitempty"`
	StreetNames                      []string `json:"street_names,omitempty"`
	Time                             int64    `json:"time"`
	Length                           float64  `json:"length"`
	BeginShapeIndex                  int      `json:"begin_shape_index,omitempty"`
	EndShapeIndex                    int      `json:"end_shape_index,omitempty"`
	TravelMode                       string   `json:"travel_mode,omitempty"`
	TravelType                       string   `json:"travel_type,omitempty"`
	BearingBefore                    int      `json:"bearing_before,omitempty"`
	BearingAfter                     int      `json:"bearing_after,omitempty"`
}

type RoutePlan struct {
	Feasible             bool               `json:"feasible"`
	InfeasibilityReasons []string           `json:"infeasibility_reasons,omitempty"`
	VehicleID            string             `json:"vehicle_id,omitempty"`
	VehicleType          string             `json:"vehicle_type,omitempty"`
	DepartureTime        time.Time          `json:"departure_time"`
	CompletionTime       time.Time          `json:"completion_time"`
	ETASeconds           int64              `json:"eta_seconds"`
	TotalDistanceKm      float64            `json:"total_distance_km"`
	TotalDurationSeconds int64              `json:"total_duration_seconds"`
	OrderedSteps         []RouteStep        `json:"ordered_steps"`
	AlternateRoutes      []RouteAlternative `json:"alternate_routes,omitempty"`
}

type RouteAlternative struct {
	RouteIndex           int         `json:"route_index"`
	CompletionTime       time.Time   `json:"completion_time"`
	ETASeconds           int64       `json:"eta_seconds"`
	TotalDistanceKm      float64     `json:"total_distance_km"`
	TotalDurationSeconds int64       `json:"total_duration_seconds"`
	OrderedSteps         []RouteStep `json:"ordered_steps"`
}

type OptimizeRouteCommand struct {
	TenantID      string
	JobIDs        []string
	Jobs          []domain.Job
	Origin        RouteLocation
	Vehicle       RouteVehicleProfile
	DriverShift   DriverShift
	DepartureTime *time.Time
}

type RoutingUseCase struct {
	optimizer     RouteOptimizer
	repository    JobRepository
	clock         Clock
	defaultTenant string
}

func NewRoutingUseCase(repository JobRepository, clock Clock, defaultTenant string, optimizer ...RouteOptimizer) *RoutingUseCase {
	var selectedOptimizer RouteOptimizer
	if len(optimizer) > 0 {
		selectedOptimizer = optimizer[0]
	}
	return &RoutingUseCase{
		repository:    repository,
		clock:         clock,
		defaultTenant: defaultTenant,
		optimizer:     selectedOptimizer,
	}
}

func (u *RoutingUseCase) OptimizeRoute(ctx context.Context, command OptimizeRouteCommand) (RoutePlan, error) {
	if len(command.JobIDs) == 0 {
		return RoutePlan{}, errors.New("at least one job_id is required")
	}

	jobs := append([]domain.Job(nil), command.Jobs...)
	if len(jobs) == 0 {
		jobs = make([]domain.Job, 0, len(command.JobIDs))
		for _, jobID := range command.JobIDs {
			job, err := u.findJob(command.TenantID, jobID)
			if err != nil {
				return RoutePlan{}, err
			}
			jobs = append(jobs, job)
		}
	}

	departure := u.resolveDepartureTime(command.DepartureTime, command.DriverShift)
	command.Jobs = jobs
	command.DepartureTime = &departure

	if u.optimizer != nil {
		return u.optimizer.OptimizeRoute(ctx, command)
	}

	return u.planRoute(jobs, command.Origin, command.Vehicle, command.DriverShift, &departure)
}

func (u *RoutingUseCase) findJob(tenantID, jobID string) (domain.Job, error) {
	resolvedTenant := strings.TrimSpace(tenantID)
	if resolvedTenant == "" {
		resolvedTenant = u.defaultTenant
	}
	job, ok := u.repository.FindByTenantAndID(resolvedTenant, strings.TrimSpace(jobID))
	if !ok {
		return domain.Job{}, ErrJobNotFound
	}
	return job, nil
}

func (u *RoutingUseCase) planRoute(
	jobs []domain.Job,
	origin RouteLocation,
	vehicle RouteVehicleProfile,
	driverShift DriverShift,
	departureTime *time.Time,
) (RoutePlan, error) {
	start := normalizeRouteLocation(origin)
	if !hasCoordinates(start.Latitude, start.Longitude) {
		return RoutePlan{}, errors.New("origin coordinates are required")
	}
	if len(jobs) == 0 {
		return RoutePlan{}, errors.New("at least one job is required")
	}

	for _, job := range jobs {
		if _, err := stopToLocation("pickup", job.Pickup); err != nil {
			return RoutePlan{}, fmt.Errorf("job %s pickup: %w", job.ID, err)
		}
		if _, err := stopToLocation("dropoff", job.Dropoff); err != nil {
			return RoutePlan{}, fmt.Errorf("job %s dropoff: %w", job.ID, err)
		}
	}

	departure := u.resolveDepartureTime(departureTime, driverShift)
	reasons := collectFeasibilityIssues(jobs, vehicle, departure, driverShift)
	feasible := len(reasons) == 0

	remaining := append([]domain.Job{}, jobs...)
	current := start
	currentTime := departure
	steps := make([]RouteStep, 0, len(jobs)*2)
	totalDistance := 0.0
	var totalDuration int64

	for len(remaining) > 0 {
		sort.SliceStable(remaining, func(i, j int) bool {
			return routeSortScore(current, remaining[i]) < routeSortScore(current, remaining[j])
		})
		job := remaining[0]
		remaining = remaining[1:]

		pickup, _ := stopToLocation("pickup", job.Pickup)
		dropoff, _ := stopToLocation("dropoff", job.Dropoff)

		pickupDistance := haversineKm(current.Latitude, current.Longitude, pickup.Latitude, pickup.Longitude)
		pickupTravelSeconds := estimateTravelSeconds(pickupDistance, vehicle.AverageSpeedKPH)
		pickupArrival := currentTime.Add(time.Duration(pickupTravelSeconds) * time.Second)
		pickupDeparture := pickupArrival

		totalDistance += pickupDistance
		totalDuration += pickupTravelSeconds

		steps = append(steps, RouteStep{
			Sequence:               len(steps) + 1,
			JobID:                  job.ID,
			StopType:               "pickup",
			Address:                pickup.Address,
			Label:                  pickup.Label,
			Latitude:               pickup.Latitude,
			Longitude:              pickup.Longitude,
			DistanceFromPreviousKm: roundDistance(pickupDistance),
			TravelSecondsFromPrev:  pickupTravelSeconds,
			ArrivalAt:              pickupArrival,
			DepartureAt:            pickupDeparture,
			ETASeconds:             int64(pickupArrival.Sub(departure).Seconds()),
		})

		serviceDuration := time.Duration(job.EstimatedServiceMinutes) * time.Minute
		currentTime = pickupDeparture.Add(serviceDuration)

		dropoffDistance := haversineKm(pickup.Latitude, pickup.Longitude, dropoff.Latitude, dropoff.Longitude)
		dropoffTravelSeconds := estimateTravelSeconds(dropoffDistance, vehicle.AverageSpeedKPH)
		dropoffArrival := currentTime.Add(time.Duration(dropoffTravelSeconds) * time.Second)
		dropoffDeparture := dropoffArrival.Add(serviceDuration)

		totalDistance += dropoffDistance
		totalDuration += dropoffTravelSeconds + int64(serviceDuration.Seconds())

		steps = append(steps, RouteStep{
			Sequence:               len(steps) + 1,
			JobID:                  job.ID,
			StopType:               "dropoff",
			Address:                dropoff.Address,
			Label:                  dropoff.Label,
			Latitude:               dropoff.Latitude,
			Longitude:              dropoff.Longitude,
			DistanceFromPreviousKm: roundDistance(dropoffDistance),
			TravelSecondsFromPrev:  dropoffTravelSeconds,
			ArrivalAt:              dropoffArrival,
			DepartureAt:            dropoffDeparture,
			ETASeconds:             int64(dropoffArrival.Sub(departure).Seconds()),
		})

		current = dropoff
		currentTime = dropoffDeparture
	}

	if driverShift.End != nil && currentTime.After(driverShift.End.UTC()) {
		feasible = false
		reasons = append(reasons, "route exceeds driver shift end")
	}

	return RoutePlan{
		Feasible:             feasible,
		InfeasibilityReasons: uniqueStrings(reasons),
		VehicleID:            strings.TrimSpace(vehicle.VehicleID),
		VehicleType:          strings.TrimSpace(vehicle.VehicleType),
		DepartureTime:        departure,
		CompletionTime:       currentTime,
		ETASeconds:           int64(currentTime.Sub(departure).Seconds()),
		TotalDistanceKm:      roundDistance(totalDistance),
		TotalDurationSeconds: totalDuration,
		OrderedSteps:         steps,
	}, nil
}

func collectFeasibilityIssues(jobs []domain.Job, vehicle RouteVehicleProfile, departure time.Time, driverShift DriverShift) []string {
	reasons := make([]string, 0)
	vehicleType := strings.TrimSpace(strings.ToLower(vehicle.VehicleType))

	for _, job := range jobs {
		requiredType := strings.TrimSpace(strings.ToLower(job.RequiredVehicleType))
		if requiredType != "" && vehicleType != "" && requiredType != vehicleType {
			reasons = append(reasons, fmt.Sprintf("job %s requires vehicle type %s", job.ID, job.RequiredVehicleType))
		}
		if vehicle.CapacityWeightKg > 0 && job.LoadWeightKg > vehicle.CapacityWeightKg {
			reasons = append(reasons, fmt.Sprintf("job %s exceeds vehicle weight capacity", job.ID))
		}
		if vehicle.CapacityVolumeM3 > 0 && job.LoadVolumeM3 > vehicle.CapacityVolumeM3 {
			reasons = append(reasons, fmt.Sprintf("job %s exceeds vehicle volume capacity", job.ID))
		}
	}

	if driverShift.Start != nil && departure.Before(driverShift.Start.UTC()) {
		reasons = append(reasons, "departure is before driver shift start")
	}
	return uniqueStrings(reasons)
}

func routeSortScore(origin RouteLocation, job domain.Job) float64 {
	pickup, err := stopToLocation("pickup", job.Pickup)
	if err != nil {
		return math.MaxFloat64
	}
	score := haversineKm(origin.Latitude, origin.Longitude, pickup.Latitude, pickup.Longitude)
	switch job.Priority {
	case domain.PriorityUrgent:
		score -= 20
	case domain.PriorityHigh:
		score -= 10
	case domain.PriorityLow:
		score += 5
	}
	return score
}

func stopToLocation(label string, stop domain.JobStop) (RouteLocation, error) {
	if stop.Latitude == nil || stop.Longitude == nil {
		return RouteLocation{}, errors.New("coordinates are required")
	}
	return RouteLocation{
		Label:     label,
		Address:   stop.Address,
		Latitude:  *stop.Latitude,
		Longitude: *stop.Longitude,
	}, nil
}

func normalizeRouteLocation(location RouteLocation) RouteLocation {
	location.Label = strings.TrimSpace(location.Label)
	location.Address = strings.TrimSpace(location.Address)
	return location
}

func hasCoordinates(latitude, longitude float64) bool {
	return latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180 && !(latitude == 0 && longitude == 0)
}

func estimateTravelSeconds(distanceKm, speedKPH float64) int64 {
	if speedKPH <= 0 {
		speedKPH = defaultAverageSpeedKPH
	}
	hours := distanceKm / speedKPH
	return int64(math.Round(hours * 3600))
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func roundDistance(distanceKm float64) float64 {
	return math.Round(distanceKm*100) / 100
}

func (u *RoutingUseCase) resolveDepartureTime(departureTime *time.Time, shift DriverShift) time.Time {
	if departureTime != nil && !departureTime.IsZero() {
		return departureTime.UTC()
	}
	departure := u.clock.Now().UTC()
	if shift.Start != nil && departure.Before(shift.Start.UTC()) {
		return shift.Start.UTC()
	}
	return departure
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
