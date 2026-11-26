package dispatchhttp

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type fakeRoadRouter struct {
	got []application.RoutePoint
	err error
}

func (f *fakeRoadRouter) Preview(_ context.Context, points []application.RoutePoint) (application.RoutePreview, error) {
	f.got = points
	return application.RoutePreview{Shapes: []string{"abc"}, ShapeEncoding: "polyline6", DistanceKm: 12.8, DurationSeconds: 1094}, f.err
}

func (f *fakeRoadRouter) Matrix(_ context.Context, points []application.RoutePoint) (application.RouteMatrix, error) {
	f.got = points
	km := 2.9
	return application.RouteMatrix{DistancesKm: [][]*float64{{nil, &km}, {&km, nil}}}, f.err
}

func roadRoutesMux(router application.RoadRouter) *http.ServeMux {
	handler := buildDispatchTestHandler()
	if router != nil {
		handler = handler.WithRoadRouter(router)
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	return mux
}

const twoLagosPoints = `{"points":[{"latitude":6.4541,"longitude":3.3947},{"latitude":6.5248,"longitude":3.3516}]}`

func TestRoutePreview_ReturnsTheRouterAnswer(t *testing.T) {
	router := &fakeRoadRouter{}
	res := dispatchRequest(t, roadRoutesMux(router), http.MethodPost, "/dispatch/routes/preview", twoLagosPoints)
	if res.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", res.Code, res.Body.String())
	}
	var preview application.RoutePreview
	decodeData(t, res.Body.Bytes(), &preview)
	if preview.DistanceKm != 12.8 || len(preview.Shapes) != 1 || len(router.got) != 2 {
		t.Fatalf("unexpected preview %+v, points %+v", preview, router.got)
	}
}

func TestRouteMatrix_KeepsUnreachablePairsNull(t *testing.T) {
	res := dispatchRequest(t, roadRoutesMux(&fakeRoadRouter{}), http.MethodPost, "/dispatch/routes/matrix", twoLagosPoints)
	if res.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", res.Code, res.Body.String())
	}
	var matrix application.RouteMatrix
	decodeData(t, res.Body.Bytes(), &matrix)
	if matrix.DistancesKm[0][0] != nil || *matrix.DistancesKm[0][1] != 2.9 {
		t.Fatalf("unexpected matrix %+v", matrix)
	}
}

func TestRoadRoutes_RejectBadInputAndMapRouterErrors(t *testing.T) {
	cases := []struct {
		name   string
		router application.RoadRouter
		path   string
		body   string
		want   int
	}{
		{"one point", &fakeRoadRouter{}, "/dispatch/routes/preview", `{"points":[{"latitude":6.4,"longitude":3.3}]}`, http.StatusBadRequest},
		{"null island", &fakeRoadRouter{}, "/dispatch/routes/preview", `{"points":[{"latitude":0,"longitude":0},{"latitude":6.5,"longitude":3.3}]}`, http.StatusBadRequest},
		{"too many for a matrix", &fakeRoadRouter{}, "/dispatch/routes/matrix", manyPoints(application.MaxMatrixPoints + 1), http.StatusBadRequest},
		{"no road", &fakeRoadRouter{err: fmt.Errorf("%w: no path", application.ErrNoRoadRoute)}, "/dispatch/routes/preview", twoLagosPoints, http.StatusUnprocessableEntity},
		{"router down", &fakeRoadRouter{err: fmt.Errorf("connection refused")}, "/dispatch/routes/matrix", twoLagosPoints, http.StatusBadGateway},
		{"not configured", nil, "/dispatch/routes/preview", twoLagosPoints, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := dispatchRequest(t, roadRoutesMux(c.router), http.MethodPost, c.path, c.body)
			if res.Code != c.want {
				t.Fatalf("status %d, want %d body=%s", res.Code, c.want, res.Body.String())
			}
		})
	}
}

func manyPoints(n int) string {
	body := `{"points":[`
	for i := 0; i < n; i++ {
		if i > 0 {
			body += ","
		}
		body += fmt.Sprintf(`{"latitude":6.%d,"longitude":3.3}`, 40+i)
	}
	return body + `]}`
}
