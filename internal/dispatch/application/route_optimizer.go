package application

import "context"

type RouteOptimizer interface {
	OptimizeRoute(ctx context.Context, cmd OptimizeRouteCommand) (RoutePlan, error)
}
