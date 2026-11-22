package application

import (
	"context"
	"testing"
	"time"
)

type fakeTrackStore struct{ points []TrackPoint }

func (f fakeTrackStore) VehicleTrack(_ context.Context, _, _ string, _, _ time.Time, _ int) ([]TrackPoint, error) {
	return f.points, nil
}

func TestSummariseTrack(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	// ~0.1 degree of latitude is ~11.1 km; two hops over 30 minutes.
	points := []TrackPoint{
		{At: t0, Latitude: 6.5, Longitude: 3.3},
		{At: t0.Add(15 * time.Minute), Latitude: 6.55, Longitude: 3.3},
		{At: t0.Add(16 * time.Minute), Latitude: 7.5, Longitude: 3.3}, // GPS jump, ignored
		{At: t0.Add(30 * time.Minute), Latitude: 6.6, Longitude: 3.3},
	}

	stats := SummariseTrack(points)

	if stats.DistanceKM == nil || *stats.DistanceKM < 10.5 || *stats.DistanceKM > 11.5 {
		t.Fatalf("distance = %v, want ~11.1 km", stats.DistanceKM)
	}
	if stats.DurationMinutes == nil || *stats.DurationMinutes != 30 {
		t.Fatalf("duration = %v, want 30", stats.DurationMinutes)
	}
	if stats.AvgSpeedKPH == nil || *stats.AvgSpeedKPH < 21 || *stats.AvgSpeedKPH > 23 {
		t.Fatalf("avg speed = %v, want ~22", stats.AvgSpeedKPH)
	}
	if stats.StartedAt == nil || *stats.StartedAt != "2026-11-22T08:00:00Z" {
		t.Fatalf("started = %v", stats.StartedAt)
	}
}

func TestSummariseTrack_TooFewPoints(t *testing.T) {
	if s := SummariseTrack(nil); s.StartedAt != nil || s.DistanceKM != nil {
		t.Fatalf("empty track should have no stats: %+v", s)
	}
	one := SummariseTrack([]TrackPoint{{At: time.Now(), Latitude: 1, Longitude: 1}})
	if one.StartedAt == nil || one.DistanceKM != nil {
		t.Fatalf("one point gives a start only: %+v", one)
	}
}

func TestTripStats_RefusesBadWindow(t *testing.T) {
	uc := NewTripStatsUseCase(fakeTrackStore{}, fixedTrackingClock{now: time.Now()})
	from := time.Now()
	to := from.Add(-time.Hour)
	if _, err := uc.Stats(context.Background(), "t1", []TripWindow{{ID: "j1", VehicleID: "v1", From: from, To: &to}}); err != ErrInvalidTripWindow {
		t.Fatalf("err = %v, want ErrInvalidTripWindow", err)
	}
}
