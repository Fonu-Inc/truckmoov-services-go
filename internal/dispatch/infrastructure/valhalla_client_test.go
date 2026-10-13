package infrastructure

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/truckmoov/services-go/internal/dispatch/application"
)

func matrixClient(t *testing.T, body string, onRequest func(query string)) *ValhallaClient {
	t.Helper()
	return NewValhallaClientWithConfig("http://valhalla.test", &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			query, err := url.QueryUnescape(r.URL.Query().Get("json"))
			if err != nil {
				t.Fatalf("unescape query json: %v", err)
			}
			if onRequest != nil {
				onRequest(query)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	})
}

func twoNodes() []SolverNodeRef {
	return []SolverNodeRef{
		{location: application.RouteLocation{Latitude: 6.45, Longitude: 3.39}},
		{location: application.RouteLocation{Latitude: 6.60, Longitude: 3.35}},
	}
}

func TestGetMatrix_NullTimeIsUnreachable(t *testing.T) {
	body := `{"sources_to_targets":[[{"time":0},{"time":null}],[{"time":612.4},{"time":0}]]}`
	client := matrixClient(t, body, nil)

	matrix, err := client.GetMatrix(context.Background(), twoNodes())
	if err != nil {
		t.Fatalf("GetMatrix: %v", err)
	}

	want := []int64{0, unreachableSeconds, 612, 0}
	for i := range want {
		if matrix[i] != want[i] {
			t.Fatalf("matrix[%d] = %d, want %d (matrix %v)", i, matrix[i], want[i], matrix)
		}
	}
}

func TestGetMatrix_SendsTruckTopSpeed(t *testing.T) {
	body := `{"sources_to_targets":[[{"time":0},{"time":1}],[{"time":1},{"time":0}]]}`
	var sent string
	client := matrixClient(t, body, func(query string) { sent = query })

	if _, err := client.GetMatrix(context.Background(), twoNodes()); err != nil {
		t.Fatalf("GetMatrix: %v", err)
	}

	if !strings.Contains(sent, `"costing_options":{"truck":{"top_speed":80}}`) {
		t.Fatalf("request missing truck top_speed: %s", sent)
	}
}
