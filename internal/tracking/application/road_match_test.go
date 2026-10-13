package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/domain"
)

type stubHistory struct{ newestFirst []LatestLocation }

func (s stubHistory) Append(LatestLocation) error { return nil }

func (s stubHistory) History(_, _ string, limit int) ([]LatestLocation, error) {
	return s.newestFirst[:min(limit, len(s.newestFirst))], nil
}

type stubMatcher struct {
	match RoadMatch
	ok    bool
	trace []LatestLocation
}

func (s *stubMatcher) MatchLast(_ context.Context, trace []LatestLocation) (RoadMatch, bool, error) {
	s.trace = trace
	return s.match, s.ok, nil
}

type stubRoadPublisher struct{ published chan domain.RoadMatched }

func (s stubRoadPublisher) PublishRoadMatched(_ context.Context, event domain.RoadMatched) error {
	s.published <- event
	return nil
}

var roadMatchNow = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

func fixAt(id string, ago time.Duration) LatestLocation {
	return LatestLocation{EventID: id, Latitude: 6.5, Longitude: 3.3, ObservedAt: roadMatchNow.Add(-ago)}
}

func moving(speed float64) domain.LocationUpdate {
	return domain.LocationUpdate{
		EventID:    "evt-now",
		OccurredAt: roadMatchNow,
		TenantID:   "t1",
		VehicleID:  "v1",
		DeviceID:   "d1",
		Latitude:   6.51,
		Longitude:  3.31,
		SpeedKPH:   &speed,
		FixTimeRFC: roadMatchNow.Format(time.RFC3339),
	}
}

func newTestRoadMatch(history stubHistory, matcher *stubMatcher) (*RoadMatchService, stubRoadPublisher) {
	publisher := stubRoadPublisher{published: make(chan domain.RoadMatched, 1)}
	service := NewRoadMatchService(history, matcher, publisher, fixedTrackingClock{now: roadMatchNow},
		fixedTrackingIDGenerator{value: "evt-road"}, RoadMatchConfig{
			MinSpeedKPH: 20, TracePoints: 8, MaxTraceAge: 3 * time.Minute, Timeout: time.Second, Concurrency: 2,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return service, publisher
}

func TestRoadMatch_PublishesTheMatchedRoad(t *testing.T) {
	limit := 50
	matcher := &stubMatcher{ok: true, match: RoadMatch{
		Latitude: 6.5101, Longitude: 3.3102, RoadClass: "primary", RoadName: "Ikorodu Road", OSMSpeedLimitKPH: &limit, WayID: 42,
	}}
	history := stubHistory{newestFirst: []LatestLocation{
		fixAt("evt-now", 0), fixAt("evt-2", 20*time.Second), fixAt("evt-1", 40*time.Second), fixAt("evt-old", 10*time.Minute),
	}}
	service, publisher := newTestRoadMatch(history, matcher)

	if err := service.match(context.Background(), moving(64)); err != nil {
		t.Fatalf("match: %v", err)
	}
	event := <-publisher.published

	ids := []string{}
	for _, fix := range matcher.trace {
		ids = append(ids, fix.EventID)
	}
	if got, want := ids, []string{"evt-1", "evt-2", "evt-now"}; !equalStrings(got, want) {
		t.Fatalf("trace = %v, want oldest first, recent only, ending at the fix: %v", got, want)
	}
	if event.Subject() != "tm.t1.vehicle.road.matched" || event.RoadName != "Ikorodu Road" || event.SpeedKPH != 64 ||
		event.Latitude != 6.5101 || *event.OSMSpeedLimitKPH != 50 || event.WayID != 42 {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestRoadMatch_NeedsATraceAndAMatch(t *testing.T) {
	alone := stubHistory{newestFirst: []LatestLocation{fixAt("evt-now", 0)}}
	service, publisher := newTestRoadMatch(alone, &stubMatcher{ok: true})
	if err := service.match(context.Background(), moving(64)); err != nil || len(publisher.published) != 0 {
		t.Fatalf("a single fix is no trace: err=%v published=%d", err, len(publisher.published))
	}

	trace := stubHistory{newestFirst: []LatestLocation{fixAt("evt-now", 0), fixAt("evt-1", 20*time.Second)}}
	service, publisher = newTestRoadMatch(trace, &stubMatcher{ok: false})
	if err := service.match(context.Background(), moving(64)); err != nil || len(publisher.published) != 0 {
		t.Fatalf("an unmatched fix publishes nothing: err=%v published=%d", err, len(publisher.published))
	}
}

func TestRoadMatch_SkipsSlowFixes(t *testing.T) {
	matcher := &stubMatcher{ok: true}
	service, _ := newTestRoadMatch(stubHistory{}, matcher)

	service.Submit(moving(5))
	stopped := moving(0)
	stopped.SpeedKPH = nil
	service.Submit(stopped)

	if len(service.slots) != 0 || matcher.trace != nil {
		t.Fatal("slow or speedless fixes must not be matched")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
