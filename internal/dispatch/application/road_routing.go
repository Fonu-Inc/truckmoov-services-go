package application

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoRoadRoute means the router answered but no drivable route joins the points
// (off the road network, or farther apart than the router allows).
var ErrNoRoadRoute = errors.New("no road route joins these points")

// RoadRouter routes points a client supplies over the road network, for screens that plan a
// route before any job exists (the job wizard's route map and stop-order optimiser).
type RoadRouter interface {
	Preview(ctx context.Context, points []RoutePoint) (RoutePreview, error)
	Matrix(ctx context.Context, points []RoutePoint) (RouteMatrix, error)
}

// RoutePreview is the drive through the points in the order given.
type RoutePreview struct {
	// One encoded polyline per leg; joined strings would not decode as one polyline.
	Shapes          []string `json:"shapes"`
	ShapeEncoding   string   `json:"shape_encoding"`
	DistanceKm      float64  `json:"distance_km"`
	DurationSeconds int64    `json:"duration_seconds"`
}

// RouteMatrix holds every point-to-point drive; a null cell means no road connects the pair.
type RouteMatrix struct {
	DistancesKm      [][]*float64 `json:"distances_km"`
	DurationsSeconds [][]*int64   `json:"durations_seconds"`
}

// Valhalla routes at most 20 locations at once; a matrix costs n² routes, so it stays small.
const (
	MaxPreviewPoints = 20
	MaxMatrixPoints  = 12
)

// ValidateRoutePoints rejects too few or too many points and coordinates off the globe.
func ValidateRoutePoints(points []RoutePoint, max int) error {
	if len(points) < 2 {
		return fmt.Errorf("at least two points are required")
	}
	if len(points) > max {
		return fmt.Errorf("at most %d points are allowed", max)
	}
	for i, p := range points {
		if p.Latitude < -90 || p.Latitude > 90 || p.Longitude < -180 || p.Longitude > 180 {
			return fmt.Errorf("point %d has invalid coordinates", i)
		}
		if p.Latitude == 0 && p.Longitude == 0 {
			return fmt.Errorf("point %d has no coordinates", i)
		}
	}
	return nil
}
