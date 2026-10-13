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

func (s *ValhallaRouteShaper) RouteShape(
	ctx context.Context,
	points []application.RoutePoint,
) (string, string, error) {
	locations := make([]ValhallaLocStruct, 0, len(points))
	for _, point := range points {
		locations = append(locations, ValhallaLocStruct{Lat: point.Latitude, Lon: point.Longitude})
	}
	return s.client.RouteShape(ctx, locations)
}

// RouteEstimate returns the duration and distance Valhalla plans for the drive
// from its road speeds; no live traffic is applied. Used to work out how much
// of a late delivery the route itself explains.
func (s *ValhallaRouteShaper) RouteEstimate(
	ctx context.Context,
	points []application.RoutePoint,
) (int64, float64, error) {
	locations := make([]ValhallaLocStruct, 0, len(points))
	for _, point := range points {
		locations = append(locations, ValhallaLocStruct{Lat: point.Latitude, Lon: point.Longitude})
	}
	return s.client.RouteEstimate(ctx, locations)
}
