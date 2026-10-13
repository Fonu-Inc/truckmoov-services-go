package infrastructure

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/pkg/valhalla"
)

// Valhalla reports 255 for an unlimited road and 0 when OSM has no limit.
const unlimitedSpeed = 255

type ValhallaRoadMatcher struct {
	client *valhalla.Client
}

func NewValhallaRoadMatcher(client *valhalla.Client) *ValhallaRoadMatcher {
	return &ValhallaRoadMatcher{client: client}
}

func (m *ValhallaRoadMatcher) MatchLast(
	ctx context.Context,
	trace []application.LatestLocation,
) (application.RoadMatch, bool, error) {
	result, err := m.client.TraceAttributes(ctx, tracePoints(trace))
	if err != nil {
		return application.RoadMatch{}, false, err
	}
	if len(result.MatchedPoints) == 0 {
		return application.RoadMatch{}, false, nil
	}
	last := result.MatchedPoints[len(result.MatchedPoints)-1]
	if last.Type == "unmatched" || last.EdgeIndex == nil || *last.EdgeIndex >= len(result.Edges) {
		return application.RoadMatch{}, false, nil
	}
	edge := result.Edges[*last.EdgeIndex]
	return application.RoadMatch{
		Latitude:         last.Lat,
		Longitude:        last.Lon,
		RoadClass:        edge.RoadClass,
		RoadName:         firstName(edge.Names),
		OSMSpeedLimitKPH: postedLimit(int(edge.SpeedLimit)),
		WayID:            edge.WayID,
	}, true, nil
}

func tracePoints(trace []application.LatestLocation) []valhalla.TracePoint {
	points := make([]valhalla.TracePoint, 0, len(trace))
	for _, fix := range trace {
		at := fix.ObservedAt
		if parsed, err := time.Parse(time.RFC3339, fix.FixTimeRFC); err == nil {
			at = parsed
		}
		points = append(points, valhalla.TracePoint{Lat: fix.Latitude, Lon: fix.Longitude, Time: at.Unix()})
	}
	return points
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func postedLimit(kph int) *int {
	if kph <= 0 || kph >= unlimitedSpeed {
		return nil
	}
	return &kph
}
