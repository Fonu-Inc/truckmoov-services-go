package infrastructure

import (
	"context"

	"github.com/truckmoov/services-go/internal/dispatch/application"
)

type ValhallaRouteShaper struct {
	client *ValhallaClient
}

func NewValhallaRouteShaper(client *ValhallaClient) *ValhallaRouteShaper {
	return &ValhallaRouteShaper{client: client}
}

func valhallaLocations(points []application.RoutePoint) []ValhallaLocStruct {
	locations := make([]ValhallaLocStruct, 0, len(points))
	for _, point := range points {
		locations = append(locations, ValhallaLocStruct{Lat: point.Latitude, Lon: point.Longitude})
	}
	return locations
}

func (s *ValhallaRouteShaper) RouteShape(
	ctx context.Context,
	points []application.RoutePoint,
) (string, string, error) {
	return s.client.RouteShape(ctx, valhallaLocations(points))
}

// Preview and Matrix serve routes a client plans before any job exists.
func (s *ValhallaRouteShaper) Preview(ctx context.Context, points []application.RoutePoint) (application.RoutePreview, error) {
	shapes, seconds, km, err := s.client.RoutePreview(ctx, valhallaLocations(points))
	if err != nil {
		return application.RoutePreview{}, err
	}
	return application.RoutePreview{Shapes: shapes, ShapeEncoding: "polyline6", DistanceKm: km, DurationSeconds: seconds}, nil
}

func (s *ValhallaRouteShaper) Matrix(ctx context.Context, points []application.RoutePoint) (application.RouteMatrix, error) {
	km, seconds, err := s.client.DistanceMatrix(ctx, valhallaLocations(points))
	if err != nil {
		return application.RouteMatrix{}, err
	}
	return application.RouteMatrix{DistancesKm: km, DurationsSeconds: seconds}, nil
}

// RouteEstimate returns the duration and distance Valhalla plans for the drive
// from its road speeds; no live traffic is applied. Used to work out how much
// of a late delivery the route itself explains.
func (s *ValhallaRouteShaper) RouteEstimate(
	ctx context.Context,
	points []application.RoutePoint,
) (int64, float64, error) {
	return s.client.RouteEstimate(ctx, valhallaLocations(points))
}
