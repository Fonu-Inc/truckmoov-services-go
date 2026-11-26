package dispatchhttp

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

// WithRoadRouter enables /dispatch/routes/preview and /dispatch/routes/matrix; without it they report
// unconfigured, so a deployment without Valhalla still starts.
func (h *Handler) WithRoadRouter(router application.RoadRouter) *Handler {
	h.roadRouter = router
	return h
}

type routePointsRequest struct {
	Points []application.RoutePoint `json:"points"`
}

func (h *Handler) routePreview(w http.ResponseWriter, r *http.Request) {
	points, ok := h.routePoints(w, r, application.MaxPreviewPoints)
	if !ok {
		return
	}
	preview, err := h.roadRouter.Preview(r.Context(), points)
	if err != nil {
		writeRoadRouteError(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, preview)
}

func (h *Handler) routeMatrix(w http.ResponseWriter, r *http.Request) {
	points, ok := h.routePoints(w, r, application.MaxMatrixPoints)
	if !ok {
		return
	}
	matrix, err := h.roadRouter.Matrix(r.Context(), points)
	if err != nil {
		writeRoadRouteError(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, matrix)
}

func (h *Handler) routePoints(w http.ResponseWriter, r *http.Request, max int) ([]application.RoutePoint, bool) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return nil, false
	}
	if h.roadRouter == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "road routing is not configured")
		return nil, false
	}
	var req routePointsRequest
	if err := decodeJSONBody(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return nil, false
	}
	if err := application.ValidateRoutePoints(req.Points, max); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return req.Points, true
}

func writeRoadRouteError(w http.ResponseWriter, err error) {
	if errors.Is(err, application.ErrNoRoadRoute) {
		httpx.WriteError(w, http.StatusUnprocessableEntity, application.ErrNoRoadRoute.Error())
		return
	}
	slog.Error("road_routing_failed", "error", err)
	httpx.WriteError(w, http.StatusBadGateway, "road routing is unavailable")
}
