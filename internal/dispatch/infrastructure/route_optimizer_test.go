package infrastructure

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	solverv1 "github.com/truckmoov/services-go/api/proto/gen/proto/solver/v1"
	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func TestBuildRoutePlanFromSolution_IncludesTurnByTurnManeuvers(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/route" {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}

			body, err := json.Marshal(map[string]any{
				"trip": map[string]any{
					"summary": map[string]any{
						"time":   4948,
						"length": 60.57,
					},
					"legs": []map[string]any{
						{
							"summary": map[string]any{"time": 1128, "length": 10.85},
							"maneuvers": []map[string]any{
								{"type": 1, "instruction": "Head west.", "time": 60, "length": 0.2, "travel_mode": "drive", "travel_type": "truck"},
								{"type": 10, "instruction": "Turn right onto Ikeja Road.", "time": 1068, "length": 10.65, "street_names": []string{"Ikeja Road"}, "travel_mode": "drive", "travel_type": "truck"},
							},
						},
						{
							"summary": map[string]any{"time": 1653, "length": 21.98},
							"maneuvers": []map[string]any{
								{"type": 8, "instruction": "Continue to Apapa.", "time": 1653, "length": 21.98, "travel_mode": "drive", "travel_type": "truck"},
							},
						},
						{
							"summary": map[string]any{"time": 1077, "length": 12.67},
							"maneuvers": []map[string]any{
								{"type": 15, "instruction": "Turn left toward Victoria Island.", "time": 1077, "length": 12.67, "travel_mode": "drive", "travel_type": "truck"},
							},
						},
						{
							"summary": map[string]any{"time": 1090, "length": 15.07},
							"maneuvers": []map[string]any{
								{"type": 4, "instruction": "You have arrived at Yaba.", "time": 1090, "length": 15.07, "travel_mode": "drive", "travel_type": "truck"},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Request:    r,
			}, nil
		}),
	}

	departure := time.Date(2026, 4, 5, 8, 0, 0, 0, time.UTC)
	cmd := application.OptimizeRouteCommand{
		Vehicle: application.RouteVehicleProfile{
			VehicleID:   "veh-2",
			VehicleType: "van",
		},
		Jobs: []domain.Job{
			{
				ID:      "job-a",
				Pickup:  domain.JobStop{Address: "Apapa", Latitude: ptrFloat(6.45), Longitude: ptrFloat(3.36)},
				Dropoff: domain.JobStop{Address: "Victoria Island", Latitude: ptrFloat(6.4281), Longitude: ptrFloat(3.4219)},
			},
			{
				ID:      "job-b",
				Pickup:  domain.JobStop{Address: "Ikeja", Latitude: ptrFloat(6.6018), Longitude: ptrFloat(3.3515)},
				Dropoff: domain.JobStop{Address: "Yaba", Latitude: ptrFloat(6.5095), Longitude: ptrFloat(3.3711)},
			},
		},
	}
	nodeRefs, _, err := buildSolverRequestData(application.RouteLocation{
		Address:   "Ojota",
		Latitude:  6.5841,
		Longitude: 3.3792,
	}, cmd.Jobs)
	if err != nil {
		t.Fatalf("build solver request data: %v", err)
	}

	resp := &solverv1.PDPSolution{
		Status: solverv1.SolveStatus_SUCCESS,
		OrderedStops: []*solverv1.Stop{
			{Index: 3, JobId: "job-b"},
			{Index: 1, JobId: "job-a"},
			{Index: 2, JobId: "job-a"},
			{Index: 4, JobId: "job-b"},
		},
	}

	plan, err := buildRoutePlanFromSolution(
		context.Background(),
		departure,
		cmd,
		nodeRefs,
		resp,
		NewValhallaClientWithConfig("http://valhalla.test", client),
	)
	if err != nil {
		t.Fatalf("build route plan from solution: %v", err)
	}

	if len(plan.OrderedSteps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(plan.OrderedSteps))
	}
	if len(plan.OrderedSteps[0].Maneuvers) != 2 {
		t.Fatalf("expected maneuvers on first step, got %+v", plan.OrderedSteps[0].Maneuvers)
	}
	if got := plan.OrderedSteps[0].Maneuvers[1].Instruction; got != "Turn right onto Ikeja Road." {
		t.Fatalf("unexpected first step maneuver instruction %q", got)
	}
	if got := plan.OrderedSteps[3].Maneuvers[0].Instruction; got != "You have arrived at Yaba." {
		t.Fatalf("unexpected last step maneuver instruction %q", got)
	}
	if plan.TotalDistanceKm != 60.57 {
		t.Fatalf("expected total distance 60.57, got %.2f", plan.TotalDistanceKm)
	}
	if plan.TotalDurationSeconds != 4948 {
		t.Fatalf("expected total duration 4948, got %d", plan.TotalDurationSeconds)
	}
}

func ptrFloat(v float64) *float64 {
	return &v
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
