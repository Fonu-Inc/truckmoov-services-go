package application

import (
	"context"
	"math"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// Free-flow speed used to work out what the drive would have taken on an empty
// road. The gap between that and the traffic-aware routing is the allowance a
// driver is not held responsible for.
const freeFlowSpeedKPH = 45.0

// RouteEstimator returns a traffic-aware duration and the distance for a drive
// through the given points in order.
type RouteEstimator interface {
	RouteEstimate(ctx context.Context, points []RoutePoint) (durationSeconds int64, distanceKm float64, err error)
}

// BuildJobPunctuality measures a delivered job against its deadline, widened by
// the traffic the drive actually ran into. A job with no deadline is not scored:
// there is nothing to be late for.
func BuildJobPunctuality(
	ctx context.Context,
	job domain.Job,
	completedAt time.Time,
	estimator RouteEstimator,
) *domain.JobPunctuality {
	if job.Deadline == nil || job.Deadline.IsZero() {
		return nil
	}

	allowance := trafficAllowanceSeconds(ctx, job, estimator)
	effectiveDeadline := job.Deadline.Add(time.Duration(allowance) * time.Second)
	lateness := int64(completedAt.Sub(effectiveDeadline).Seconds())
	if lateness < 0 {
		lateness = 0
	}

	return &domain.JobPunctuality{
		Deadline:                *job.Deadline,
		CompletedAt:             completedAt,
		TrafficAllowanceSeconds: allowance,
		LatenessSeconds:         lateness,
		OnTime:                  lateness == 0,
	}
}

// trafficAllowanceSeconds is the difference between the routed duration and the
// same distance at free-flow speed. A routing failure yields zero rather than a
// guess: an unmeasured delay is not an excused one.
func trafficAllowanceSeconds(ctx context.Context, job domain.Job, estimator RouteEstimator) int64 {
	if estimator == nil {
		return 0
	}

	points := routePointsFor(job)
	if len(points) < 2 {
		return 0
	}

	durationSeconds, distanceKm, err := estimator.RouteEstimate(ctx, points)
	if err != nil || durationSeconds <= 0 || distanceKm <= 0 {
		return 0
	}

	freeFlowSeconds := int64(math.Round((distanceKm / freeFlowSpeedKPH) * 3600))
	allowance := durationSeconds - freeFlowSeconds
	if allowance < 0 {
		return 0
	}
	return allowance
}

func routePointsFor(job domain.Job) []RoutePoint {
	stops := append([]domain.JobStop{job.Pickup}, job.Stops...)
	stops = append(stops, job.Dropoff)

	points := make([]RoutePoint, 0, len(stops))
	for _, stop := range stops {
		if stop.Latitude == nil || stop.Longitude == nil {
			continue
		}
		points = append(points, RoutePoint{Latitude: *stop.Latitude, Longitude: *stop.Longitude})
	}
	return points
}
