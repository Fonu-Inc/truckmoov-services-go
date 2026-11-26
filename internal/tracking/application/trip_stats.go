package application

import (
	"context"
	"errors"
	"math"
	"time"
)

// TrackPoint is one recorded position of a vehicle.
type TrackPoint struct {
	At        time.Time
	Latitude  float64
	Longitude float64
}

// VehicleTrackStore reads a vehicle's recorded positions, oldest first.
type VehicleTrackStore interface {
	VehicleTrack(ctx context.Context, tenantID, vehicleID string, from, to time.Time, limit int) ([]TrackPoint, error)
}

// TripWindow asks for the stats of one trip: a vehicle between two moments.
type TripWindow struct {
	ID        string     `json:"id"`
	VehicleID string     `json:"vehicle_id"`
	From      time.Time  `json:"from"`
	To        *time.Time `json:"to,omitempty"`
}

// TripStats is what the recorded positions say about one trip. Fields are nil
// when there are too few positions to tell.
type TripStats struct {
	ID              string   `json:"id"`
	VehicleID       string   `json:"vehicle_id"`
	PointCount      int      `json:"point_count"`
	StartedAt       *string  `json:"started_at,omitempty"`
	EndedAt         *string  `json:"ended_at,omitempty"`
	DistanceKM      *float64 `json:"distance_km,omitempty"`
	DurationMinutes *float64 `json:"duration_minutes,omitempty"`
	AvgSpeedKPH     *float64 `json:"avg_speed_kph,omitempty"`
}

const (
	MaxTripWindows    = 100
	maxTripWindowSpan = 31 * 24 * time.Hour
	// shortcut: a trip's track is capped at this many positions; past it the stats cover the first part only. Page the read when trips get longer.
	maxTripPoints = 20000
	// A hop faster than this between two fixes is a GPS jump, not driving.
	maxPlausibleKPH = 200.0
)

var ErrInvalidTripWindow = errors.New("each trip needs an id, a vehicle_id and a from before its to, at most 31 days apart")

type TripStatsUseCase struct {
	store VehicleTrackStore
	clock Clock
}

func NewTripStatsUseCase(store VehicleTrackStore, clock Clock) *TripStatsUseCase {
	return &TripStatsUseCase{store: store, clock: clock}
}

func (u *TripStatsUseCase) Stats(ctx context.Context, tenantID string, windows []TripWindow) ([]TripStats, error) {
	now := u.clock.Now()
	for _, w := range windows {
		to := now
		if w.To != nil {
			to = *w.To
		}
		if w.ID == "" || w.VehicleID == "" || w.From.IsZero() || !to.After(w.From) || to.Sub(w.From) > maxTripWindowSpan {
			return nil, ErrInvalidTripWindow
		}
	}
	out := make([]TripStats, 0, len(windows))
	for _, w := range windows {
		to := now
		if w.To != nil {
			to = *w.To
		}
		points, err := u.store.VehicleTrack(ctx, tenantID, w.VehicleID, w.From, to, maxTripPoints)
		if err != nil {
			return nil, err
		}
		stats := SummariseTrack(points)
		stats.ID, stats.VehicleID = w.ID, w.VehicleID
		out = append(out, stats)
	}
	return out, nil
}

// SummariseTrack turns positions (oldest first) into distance, duration and average speed.
func SummariseTrack(points []TrackPoint) TripStats {
	stats := TripStats{PointCount: len(points)}
	if len(points) == 0 {
		return stats
	}
	start := points[0].At.UTC().Format(time.RFC3339)
	stats.StartedAt = &start
	if len(points) < 2 {
		return stats
	}
	end := points[len(points)-1].At.UTC().Format(time.RFC3339)
	stats.EndedAt = &end

	km := 0.0
	// Hops are measured from the last accepted point, so one GPS jump drops only itself
	// and not the real hop after it.
	last := points[0]
	for _, p := range points[1:] {
		hop := haversineKM(last.Latitude, last.Longitude, p.Latitude, p.Longitude)
		hours := p.At.Sub(last.At).Hours()
		if hours > 0 && hop/hours > maxPlausibleKPH {
			continue
		}
		km += hop
		last = p
	}
	minutes := points[len(points)-1].At.Sub(points[0].At).Minutes()
	distance := round1(km)
	duration := round1(minutes)
	stats.DistanceKM = &distance
	stats.DurationMinutes = &duration
	if minutes > 0 {
		speed := round1(km / (minutes / 60))
		stats.AvgSpeedKPH = &speed
	}
	return stats
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
