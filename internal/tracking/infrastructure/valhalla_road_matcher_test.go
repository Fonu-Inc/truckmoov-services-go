package infrastructure

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/pkg/valhalla"
)

func valhallaStub(t *testing.T, response string, onRequest func(body map[string]any)) *ValhallaRoadMatcher {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/trace_attributes" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		if onRequest != nil {
			onRequest(body)
		}
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	return NewValhallaRoadMatcher(valhalla.NewClient(server.URL, server.Client()))
}

var twoFixes = []application.LatestLocation{
	{Latitude: 6.50, Longitude: 3.30, FixTimeRFC: "2026-10-13T09:00:00Z"},
	{Latitude: 6.51, Longitude: 3.31, FixTimeRFC: "2026-10-13T09:00:20Z"},
}

func TestValhallaRoadMatcher_DescribesTheLastPoint(t *testing.T) {
	matcher := valhallaStub(t, `{
		"edges": [
			{"names": ["Agege Motor Road"], "road_class": "secondary", "speed_limit": 0, "way_id": 1},
			{"names": ["Ikorodu Road", "A1"], "road_class": "primary", "speed_limit": 50, "way_id": 42}
		],
		"matched_points": [
			{"lat": 6.5001, "lon": 3.3001, "type": "matched", "edge_index": 0},
			{"lat": 6.5101, "lon": 3.3102, "type": "matched", "edge_index": 1}
		]
	}`, func(body map[string]any) {
		shape := body["shape"].([]any)
		if body["costing"] != "truck" || len(shape) != 2 {
			t.Errorf("unexpected request: %v", body)
		}
		if at := shape[1].(map[string]any)["time"].(float64); int64(at) != time.Date(2026, 10, 13, 9, 0, 20, 0, time.UTC).Unix() {
			t.Errorf("trace point time = %v, want the fix time", at)
		}
	})

	match, ok, err := matcher.MatchLast(context.Background(), twoFixes)
	if err != nil || !ok {
		t.Fatalf("match: ok=%v err=%v", ok, err)
	}
	if match.RoadName != "Ikorodu Road" || match.RoadClass != "primary" || match.WayID != 42 ||
		match.Latitude != 6.5101 || match.OSMSpeedLimitKPH == nil || *match.OSMSpeedLimitKPH != 50 {
		t.Fatalf("unexpected match: %+v", match)
	}
}

func TestValhallaRoadMatcher_TreatsMissingAndUnlimitedAsNoPostedLimit(t *testing.T) {
	for _, limit := range []string{"0", "255", `"unlimited"`} {
		matcher := valhallaStub(t, `{"edges":[{"names":[],"road_class":"trunk","speed_limit":`+limit+`}],
			"matched_points":[{"lat":6.5,"lon":3.3,"type":"matched","edge_index":0},{"lat":6.6,"lon":3.4,"type":"matched","edge_index":0}]}`, nil)
		match, ok, err := matcher.MatchLast(context.Background(), twoFixes)
		if err != nil || !ok || match.OSMSpeedLimitKPH != nil || match.RoadName != "" {
			t.Fatalf("speed_limit %s: ok=%v err=%v match=%+v", limit, ok, err, match)
		}
	}
}

func TestValhallaRoadMatcher_UnmatchedLastPointIsNoMatch(t *testing.T) {
	matcher := valhallaStub(t, `{"edges":[{"road_class":"primary"}],
		"matched_points":[{"lat":6.5,"lon":3.3,"type":"matched","edge_index":0},{"lat":6.6,"lon":3.4,"type":"unmatched"}]}`, nil)
	if _, ok, err := matcher.MatchLast(context.Background(), twoFixes); err != nil || ok {
		t.Fatalf("ok=%v err=%v, want no match", ok, err)
	}
}
