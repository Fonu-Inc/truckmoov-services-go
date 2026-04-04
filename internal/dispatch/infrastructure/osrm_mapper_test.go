package infrastructure

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestOsrmInputFromVroom(t *testing.T) {
	input, err := OsrmInputFromVroom(VroomResponse{
		Routes: []VroomRoute{
			{
				Steps: []VroomStep{
					{Location: []float64{3.1, 6.1}},
					{Location: []float64{3.2, 6.2}},
					{Location: []float64{3.3, 6.3}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if input.Geometries != "polyline" {
		t.Fatalf("expected polyline geometry, got %q", input.Geometries)
	}
	if input.Alternatives != true {
		t.Fatalf("expected alternatives=true, got %#v", input.Alternatives)
	}
	if input.Steps != true {
		t.Fatalf("expected steps=true")
	}
	if input.Annotations != false {
		t.Fatalf("expected annotations=false, got %#v", input.Annotations)
	}
	if input.ContinueStraight != false {
		t.Fatalf("expected continue_straight=false, got %#v", input.ContinueStraight)
	}
	if input.Profile != "" {
		t.Fatalf("expected vroom profile to be ignored, got %q", input.Profile)
	}
	if len(input.Coordinates) != 3 {
		t.Fatalf("expected 3 coordinates, got %d", len(input.Coordinates))
	}
}

func TestOsrmClientCallRouteService(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		payload, err := json.Marshal(OsrmResponse{
			Code: "Ok",
			Routes: []OsrmRoute{
				{
					Distance: 1200,
					Duration: 300,
					Geometry: Geometry{Type: "LineString"},
				},
			},
			Waypoints: []OsrmWaypoint{
				{Name: "snapped"},
			},
		})
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(payload))),
			Header:     make(http.Header),
		}, nil
	})

	client := &OsrmClient{
		baseURL: "http://osrm.test",
		httpClient: &http.Client{
			Transport: transport,
		},
	}

	resp, err := client.CallRouteService(context.Background(), OsrmInput{
		Profile:          "",
		Coordinates:      [][]float64{{3.1, 6.1}, {3.2, 6.2}, {3.3, 6.3}},
		Alternatives:     true,
		Steps:            true,
		Annotations:      false,
		Geometries:       "polyline",
		Overview:         "full",
		ContinueStraight: false,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if capturedPath != "/route/v1/driving/3.1,6.1;3.2,6.2;3.3,6.3" {
		t.Fatalf("unexpected path %q", capturedPath)
	}
	if got := capturedQuery.Get("alternatives"); got != "true" {
		t.Fatalf("expected alternatives=true, got %q", got)
	}
	if got := capturedQuery.Get("steps"); got != "true" {
		t.Fatalf("expected steps=true, got %q", got)
	}
	if got := capturedQuery.Get("annotations"); got != "false" {
		t.Fatalf("expected annotations=false, got %q", got)
	}
	if got := capturedQuery.Get("continue_straight"); got != "false" {
		t.Fatalf("expected continue_straight=false, got %q", got)
	}
	if got := capturedQuery.Get("geometries"); got != "polyline" {
		t.Fatalf("expected geometries=polyline, got %q", got)
	}

	if len(resp.Waypoints) != 3 {
		t.Fatalf("expected VROOM coordinates as 3 waypoints, got %d", len(resp.Waypoints))
	}
	if resp.Waypoints[0].Location[0] != 3.1 || resp.Waypoints[0].Location[1] != 6.1 {
		t.Fatalf("unexpected first waypoint: %#v", resp.Waypoints[0].Location)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
