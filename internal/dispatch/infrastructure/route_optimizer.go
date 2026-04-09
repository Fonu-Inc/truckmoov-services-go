package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	solverv1 "github.com/truckmoov/services-go/api/proto/gen/proto/solver/v1"
	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const defaultAverageSpeedKPH = 35.0
const defaultLegAlternates = 2

var errRouteSolverNoSolution = errors.New("route solver returned no feasible solution")

type SolverRouteOptimizer struct {
	client         solverv1.RouteSolverClient
	valhallaClient *ValhallaClient
}

func NewSolverRouteOptimizer(client solverv1.RouteSolverClient, valhallaClient *ValhallaClient) *SolverRouteOptimizer {
	return &SolverRouteOptimizer{client: client, valhallaClient: valhallaClient}
}

func (o *SolverRouteOptimizer) OptimizeRoute(
	ctx context.Context,
	cmd application.OptimizeRouteCommand,
) (application.RoutePlan, error) {
	start := normalizeRouteLocation(cmd.Origin)
	if !hasCoordinates(start.Latitude, start.Longitude) {
		return application.RoutePlan{}, errors.New("origin coordinates are required")
	}
	if len(cmd.Jobs) == 0 {
		return application.RoutePlan{}, errors.New("at least one job is required")
	}
	if cmd.DepartureTime == nil || cmd.DepartureTime.IsZero() {
		return application.RoutePlan{}, errors.New("departure time is required")
	}

	departure := cmd.DepartureTime.UTC()
	nodeRefs, solverJobs, err := buildSolverRequestData(start, cmd.Jobs)
	if err != nil {
		return application.RoutePlan{}, err
	}
	if o.valhallaClient == nil {
		return application.RoutePlan{}, errors.New("valhalla client is not configured")
	}
	matrix, err := o.valhallaClient.GetMatrix(ctx, nodeRefs)
	if err != nil {
		return application.RoutePlan{}, err
	}

	req := &solverv1.PDPRequest{
		DurationMatrix: matrix,
		MatrixSize:     int32(len(nodeRefs)),
		Jobs:           solverJobs,
	}
	if capacity, ok := vehicleCapacity(cmd.Vehicle); ok {
		req.VehicleCapacity = &capacity
	}
	if maxDuration, ok := maxRouteDuration(departure, cmd.DriverShift); ok {
		req.MaxRouteDuration = &maxDuration
	}

	resp, err := o.client.SolvePDP(ctx, req)
	if err != nil {
		return application.RoutePlan{}, fmt.Errorf("solve route over grpc: %w", err)
	}

	return buildRoutePlanFromSolution(ctx, departure, cmd, nodeRefs, resp, o.valhallaClient)
}

type SolverNodeRef struct {
	location application.RouteLocation
	jobID    string
	stopType string
}

func buildSolverRequestData(
	origin application.RouteLocation,
	jobs []domain.Job,
) ([]SolverNodeRef, []*solverv1.Job, error) {
	nodeRefs := []SolverNodeRef{{
		location: origin,
		stopType: "origin",
	}}
	solverJobs := make([]*solverv1.Job, 0, len(jobs))

	for _, job := range jobs {
		pickup, err := stopToLocation("pickup", job.Pickup)
		if err != nil {
			return nil, nil, fmt.Errorf("job %s pickup: %w", job.ID, err)
		}
		dropoff, err := stopToLocation("dropoff", job.Dropoff)
		if err != nil {
			return nil, nil, fmt.Errorf("job %s dropoff: %w", job.ID, err)
		}

		pickupIndex := int32(len(nodeRefs))
		nodeRefs = append(nodeRefs, SolverNodeRef{
			location: pickup,
			jobID:    job.ID,
			stopType: "pickup",
		})

		deliveryIndex := int32(len(nodeRefs))
		nodeRefs = append(nodeRefs, SolverNodeRef{
			location: dropoff,
			jobID:    job.ID,
			stopType: "dropoff",
		})

		solverJob := &solverv1.Job{
			Id:            job.ID,
			PickupIndex:   pickupIndex,
			DeliveryIndex: deliveryIndex,
		}
		if demand, ok := jobDemand(job); ok {
			solverJob.Demand = &demand
		}
		solverJobs = append(solverJobs, solverJob)
	}

	return nodeRefs, solverJobs, nil
}

func buildRoutePlanFromSolution(
	ctx context.Context,
	departure time.Time,
	cmd application.OptimizeRouteCommand,
	nodeRefs []SolverNodeRef,
	resp *solverv1.PDPSolution,
	valhallaClient *ValhallaClient,
) (application.RoutePlan, error) {
	if resp == nil {
		return application.RoutePlan{}, errors.New("route solver returned empty response")
	}

	reasons := collectFeasibilityIssues(cmd.Jobs, cmd.Vehicle, departure, cmd.DriverShift)
	feasible := len(reasons) == 0

	switch resp.GetStatus() {
	case solverv1.SolveStatus_SUCCESS:
	case solverv1.SolveStatus_NO_SOLUTION:
		feasible = false
		reasons = append(reasons, errRouteSolverNoSolution.Error())
	case solverv1.SolveStatus_TIMEOUT:
		feasible = false
		reasons = append(reasons, "route solver timed out")
	default:
		feasible = false
		reasons = append(reasons, "route solver returned unknown status")
	}

	if resp.GetStatus() != solverv1.SolveStatus_SUCCESS {
		completion := departure.Add(time.Duration(resp.GetTotalDuration()) * time.Second)
		if cmd.DriverShift.End != nil && completion.After(cmd.DriverShift.End.UTC()) {
			reasons = append(reasons, "route exceeds driver shift end")
		}
		return application.RoutePlan{
			Feasible:             feasible,
			InfeasibilityReasons: uniqueStrings(reasons),
			VehicleID:            strings.TrimSpace(cmd.Vehicle.VehicleID),
			VehicleType:          strings.TrimSpace(cmd.Vehicle.VehicleType),
			DepartureTime:        departure,
			CompletionTime:       completion,
			ETASeconds:           resp.GetTotalDuration(),
			TotalDurationSeconds: resp.GetTotalDuration(),
		}, nil
	}

	orderedNodes := make([]SolverNodeRef, 0, len(resp.GetOrderedStops())+1)
	orderedNodes = append(orderedNodes, nodeRefs[0])
	for _, stop := range resp.GetOrderedStops() {
		index := int(stop.GetIndex())
		if index <= 0 || index >= len(nodeRefs) {
			return application.RoutePlan{}, fmt.Errorf("route solver returned invalid stop index %d", index)
		}
		orderedNodes = append(orderedNodes, nodeRefs[index])
	}
	if valhallaClient == nil {
		return application.RoutePlan{}, errors.New("valhalla client is not configured")
	}
	routeResp, err := valhallaClient.GetTurnByTurnDirections(ctx, orderedNodes)
	if err != nil {
		return application.RoutePlan{}, err
	}
	if len(routeResp.Trip.Legs) != len(orderedNodes)-1 {
		return application.RoutePlan{}, fmt.Errorf(
			"valhalla route leg count mismatch: got %d want %d",
			len(routeResp.Trip.Legs),
			len(orderedNodes)-1,
		)
	}

	steps := make([]application.RouteStep, 0, len(resp.GetOrderedStops()))
	legAlternates := make([][]ValhallaRouteLeg, 0, len(resp.GetOrderedStops()))
	totalDistance := 0.0
	var cumulativeSeconds int64
	for i := range resp.GetOrderedStops() {
		node := orderedNodes[i+1]
		leg := routeResp.Trip.Legs[i]
		travelSeconds := int64(math.Round(leg.Summary.Time))
		distance := leg.Summary.Length
		totalDistance += distance
		cumulativeSeconds += travelSeconds
		arrival := departure.Add(time.Duration(cumulativeSeconds) * time.Second)
		alternateLegs, err := valhallaClient.GetLegAlternates(ctx, orderedNodes[i], node, defaultLegAlternates)
		if err != nil {
			return application.RoutePlan{}, err
		}
		legAlternates = append(legAlternates, alternateLegs)
		alternateShapes := make([]string, 0, len(alternateLegs))
		for _, alternateLeg := range alternateLegs {
			alternateShapes = append(alternateShapes, strings.TrimSpace(alternateLeg.Shape))
		}

		steps = append(steps, application.RouteStep{
			Sequence:               i + 1,
			JobID:                  node.jobID,
			StopType:               node.stopType,
			Address:                node.location.Address,
			Label:                  node.location.Label,
			Latitude:               node.location.Latitude,
			Longitude:              node.location.Longitude,
			DistanceFromPreviousKm: roundDistance(distance),
			TravelSecondsFromPrev:  travelSeconds,
			ArrivalAt:              arrival,
			DepartureAt:            arrival,
			ETASeconds:             cumulativeSeconds,
			Shape:                  strings.TrimSpace(leg.Shape),
			AlternateShapes:        alternateShapes,
			Maneuvers:              mapValhallaManeuvers(leg.Maneuvers),
		})
	}

	totalDuration := int64(math.Round(routeResp.Trip.Summary.Time))
	if totalDuration <= 0 {
		totalDuration = cumulativeSeconds
	}
	completion := departure.Add(time.Duration(totalDuration) * time.Second)
	if cmd.DriverShift.End != nil && completion.After(cmd.DriverShift.End.UTC()) {
		feasible = false
		reasons = append(reasons, "route exceeds driver shift end")
	}

	alternateRoutes := buildAlternateRoutes(departure, steps, legAlternates)

	return application.RoutePlan{
		Feasible:             feasible,
		InfeasibilityReasons: uniqueStrings(reasons),
		VehicleID:            strings.TrimSpace(cmd.Vehicle.VehicleID),
		VehicleType:          strings.TrimSpace(cmd.Vehicle.VehicleType),
		DepartureTime:        departure,
		CompletionTime:       completion,
		ETASeconds:           totalDuration,
		TotalDistanceKm:      roundDistance(totalDistance),
		TotalDurationSeconds: totalDuration,
		OrderedSteps:         steps,
		AlternateRoutes:      alternateRoutes,
	}, nil
}

func buildAlternateRoutes(
	departure time.Time,
	primarySteps []application.RouteStep,
	legAlternates [][]ValhallaRouteLeg,
) []application.RouteAlternative {
	maxAlternates := 0
	for _, alternates := range legAlternates {
		if len(alternates) > maxAlternates {
			maxAlternates = len(alternates)
		}
	}
	if maxAlternates == 0 {
		return nil
	}

	routes := make([]application.RouteAlternative, 0, maxAlternates)
	for alternateIndex := 0; alternateIndex < maxAlternates; alternateIndex++ {
		steps := make([]application.RouteStep, 0, len(primarySteps))
		totalDistance := 0.0
		var cumulativeSeconds int64

		for i, primaryStep := range primarySteps {
			leg := ValhallaRouteLeg{
				Summary: ValhallaRouteSummary{
					Length: primaryStep.DistanceFromPreviousKm,
					Time:   float64(primaryStep.TravelSecondsFromPrev),
				},
				Shape:     primaryStep.Shape,
				Maneuvers: makeValhallaManeuvers(primaryStep.Maneuvers),
			}
			if alternateIndex < len(legAlternates[i]) {
				leg = legAlternates[i][alternateIndex]
			}

			travelSeconds := int64(math.Round(leg.Summary.Time))
			distance := roundDistance(leg.Summary.Length)
			totalDistance += distance
			cumulativeSeconds += travelSeconds
			arrival := departure.Add(time.Duration(cumulativeSeconds) * time.Second)

			step := primaryStep
			step.DistanceFromPreviousKm = distance
			step.TravelSecondsFromPrev = travelSeconds
			step.ArrivalAt = arrival
			step.DepartureAt = arrival
			step.ETASeconds = cumulativeSeconds
			step.Shape = strings.TrimSpace(leg.Shape)
			step.Maneuvers = mapValhallaManeuvers(leg.Maneuvers)
			steps = append(steps, step)
		}

		routes = append(routes, application.RouteAlternative{
			RouteIndex:           alternateIndex + 1,
			CompletionTime:       departure.Add(time.Duration(cumulativeSeconds) * time.Second),
			ETASeconds:           cumulativeSeconds,
			TotalDistanceKm:      roundDistance(totalDistance),
			TotalDurationSeconds: cumulativeSeconds,
			OrderedSteps:         steps,
		})
	}

	return routes
}

func mapValhallaManeuvers(maneuvers []ValhallaRouteManeuver) []application.RouteManeuver {
	if len(maneuvers) == 0 {
		return nil
	}

	result := make([]application.RouteManeuver, 0, len(maneuvers))
	for _, maneuver := range maneuvers {
		result = append(result, application.RouteManeuver{
			Type:                             maneuver.Type,
			Instruction:                      strings.TrimSpace(maneuver.Instruction),
			VerbalTransitionAlertInstruction: strings.TrimSpace(maneuver.VerbalTransitionAlertInstruction),
			VerbalPreTransitionInstruction:   strings.TrimSpace(maneuver.VerbalPreTransitionInstruction),
			VerbalPostTransitionInstruction:  strings.TrimSpace(maneuver.VerbalPostTransitionInstruction),
			StreetNames:                      maneuver.StreetNames,
			Time:                             int64(math.Round(maneuver.Time)),
			Length:                           roundDistance(maneuver.Length),
			BeginShapeIndex:                  maneuver.BeginShapeIndex,
			EndShapeIndex:                    maneuver.EndShapeIndex,
			TravelMode:                       strings.TrimSpace(maneuver.TravelMode),
			TravelType:                       strings.TrimSpace(maneuver.TravelType),
			BearingBefore:                    maneuver.BearingBefore,
			BearingAfter:                     maneuver.BearingAfter,
		})
	}

	return result
}

func makeValhallaManeuvers(maneuvers []application.RouteManeuver) []ValhallaRouteManeuver {
	if len(maneuvers) == 0 {
		return nil
	}

	result := make([]ValhallaRouteManeuver, 0, len(maneuvers))
	for _, maneuver := range maneuvers {
		result = append(result, ValhallaRouteManeuver{
			Type:                             maneuver.Type,
			Instruction:                      maneuver.Instruction,
			VerbalTransitionAlertInstruction: maneuver.VerbalTransitionAlertInstruction,
			VerbalPreTransitionInstruction:   maneuver.VerbalPreTransitionInstruction,
			VerbalPostTransitionInstruction:  maneuver.VerbalPostTransitionInstruction,
			StreetNames:                      maneuver.StreetNames,
			Time:                             float64(maneuver.Time),
			Length:                           maneuver.Length,
			BeginShapeIndex:                  maneuver.BeginShapeIndex,
			EndShapeIndex:                    maneuver.EndShapeIndex,
			TravelMode:                       maneuver.TravelMode,
			TravelType:                       maneuver.TravelType,
			BearingBefore:                    maneuver.BearingBefore,
			BearingAfter:                     maneuver.BearingAfter,
		})
	}

	return result
}

func stopToLocation(label string, stop domain.JobStop) (application.RouteLocation, error) {
	if stop.Latitude == nil || stop.Longitude == nil {
		return application.RouteLocation{}, errors.New("coordinates are required")
	}
	return application.RouteLocation{
		Label:     label,
		Address:   stop.Address,
		Latitude:  *stop.Latitude,
		Longitude: *stop.Longitude,
	}, nil
}

func normalizeRouteLocation(location application.RouteLocation) application.RouteLocation {
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

func collectFeasibilityIssues(
	jobs []domain.Job,
	vehicle application.RouteVehicleProfile,
	departure time.Time,
	driverShift application.DriverShift,
) []string {
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

func vehicleCapacity(vehicle application.RouteVehicleProfile) (int32, bool) {
	if vehicle.CapacityWeightKg <= 0 {
		return 0, false
	}
	return int32(math.Ceil(vehicle.CapacityWeightKg)), true
}

func maxRouteDuration(departure time.Time, shift application.DriverShift) (int64, bool) {
	if shift.End == nil {
		return 0, false
	}
	seconds := int64(shift.End.UTC().Sub(departure).Seconds())
	if seconds <= 0 {
		return 0, false
	}
	return seconds, true
}

func jobDemand(job domain.Job) (int32, bool) {
	if job.LoadWeightKg <= 0 {
		return 0, false
	}
	return int32(math.Ceil(job.LoadWeightKg)), true
}
