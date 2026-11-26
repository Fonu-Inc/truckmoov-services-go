package infrastructure

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/truckmoov/services-go/internal/dispatch/application"
)

func valhallaReplying(status int, body string) *ValhallaClient {
	return NewValhallaClientWithConfig("http://valhalla.test", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
		}),
	})
}

var lagos = []application.RoutePoint{{Latitude: 6.45, Longitude: 3.39}, {Latitude: 6.52, Longitude: 3.35}}

func TestRoadRouter_MatrixReadsDistancesAndLeavesNoRoadNull(t *testing.T) {
	router := NewValhallaRouteShaper(valhallaReplying(200,
		`{"sources_to_targets":[[{"time":0,"distance":0},{"time":1094.4,"distance":12.78}],[{"time":null,"distance":null},{"time":0,"distance":0}]]}`))
	matrix, err := router.Matrix(context.Background(), lagos)
	if err != nil {
		t.Fatalf("Matrix: %v", err)
	}
	if *matrix.DistancesKm[0][1] != 12.78 || *matrix.DurationsSeconds[0][1] != 1094 {
		t.Fatalf("unexpected cell %v %v", *matrix.DistancesKm[0][1], *matrix.DurationsSeconds[0][1])
	}
	if matrix.DistancesKm[1][0] != nil || matrix.DurationsSeconds[1][0] != nil {
		t.Fatal("a pair with no road must stay null")
	}
}

func TestRoadRouter_PreviewReturnsLegShapesAndTotals(t *testing.T) {
	router := NewValhallaRouteShaper(valhallaReplying(200,
		`{"trip":{"legs":[{"shape":"aaa"},{"shape":"bbb"}],"summary":{"length":15.67,"time":1643.2}}}`))
	preview, err := router.Preview(context.Background(), lagos)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(preview.Shapes) != 2 || preview.ShapeEncoding != "polyline6" || preview.DistanceKm != 15.67 || preview.DurationSeconds != 1643 {
		t.Fatalf("unexpected preview %+v", preview)
	}
}

func TestRoadRouter_ValhallaRefusalIsNoRoadRoute(t *testing.T) {
	router := NewValhallaRouteShaper(valhallaReplying(400, `{"error_code":154,"error":"Path distance exceeds the max distance limit"}`))
	_, err := router.Matrix(context.Background(), lagos)
	if !errors.Is(err, application.ErrNoRoadRoute) {
		t.Fatalf("want ErrNoRoadRoute, got %v", err)
	}
	down := NewValhallaRouteShaper(valhallaReplying(503, `upstream unavailable`))
	if _, err := down.Preview(context.Background(), lagos); err == nil || errors.Is(err, application.ErrNoRoadRoute) {
		t.Fatalf("a router outage must not read as no road: %v", err)
	}
}
