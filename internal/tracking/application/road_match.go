package application

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/domain"
)

// RoadMatch is the road the last point of a trace was matched onto.
type RoadMatch struct {
	Latitude         float64
	Longitude        float64
	RoadClass        string
	RoadName         string
	OSMSpeedLimitKPH *int
	WayID            int64
}

// RoadMatcher map-matches a chronological trace and describes its last point;
// ok is false when that point matched no road.
type RoadMatcher interface {
	MatchLast(ctx context.Context, trace []LatestLocation) (match RoadMatch, ok bool, err error)
}

type RoadMatchPublisher interface {
	PublishRoadMatched(ctx context.Context, event domain.RoadMatched) error
}

type RoadMatchConfig struct {
	// MinSpeedKPH skips slow fixes: below the lowest limit nothing can be speeding.
	MinSpeedKPH float64
	TracePoints int
	MaxTraceAge time.Duration
	Timeout     time.Duration
	Concurrency int
}

// RoadMatchService works out which road each moving fix was on. It runs beside
// ingest, never in it: Valhalla is one replica, and a slow or failed match must
// not hold up the live map or a tracker's retry.
type RoadMatchService struct {
	history   LocationHistoryRepository
	matcher   RoadMatcher
	publisher RoadMatchPublisher
	clock     Clock
	ids       IDGenerator
	cfg       RoadMatchConfig
	slots     chan struct{}
	log       *slog.Logger
}

func NewRoadMatchService(
	history LocationHistoryRepository,
	matcher RoadMatcher,
	publisher RoadMatchPublisher,
	clock Clock,
	ids IDGenerator,
	cfg RoadMatchConfig,
	log *slog.Logger,
) *RoadMatchService {
	return &RoadMatchService{
		history:   history,
		matcher:   matcher,
		publisher: publisher,
		clock:     clock,
		ids:       ids,
		cfg:       cfg,
		slots:     make(chan struct{}, max(cfg.Concurrency, 1)),
		log:       log,
	}
}

// Submit matches the fix in the background. When every slot is busy the fix is
// dropped rather than queued: the next fix of a moving truck will be matched.
func (s *RoadMatchService) Submit(update domain.LocationUpdate) {
	if update.SpeedKPH == nil || *update.SpeedKPH < s.cfg.MinSpeedKPH {
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.log.Debug("road_match_dropped_busy", "vehicle", update.VehicleID)
		return
	}
	go func() {
		defer func() { <-s.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
		defer cancel()
		if err := s.match(ctx, update); err != nil {
			s.log.Warn("road_match_failed", "vehicle", update.VehicleID, "error", err)
		}
	}()
}

func (s *RoadMatchService) match(ctx context.Context, update domain.LocationUpdate) error {
	trace, err := s.recentTrace(update)
	if err != nil || len(trace) < 2 {
		return err
	}
	match, ok, err := s.matcher.MatchLast(ctx, trace)
	if err != nil || !ok {
		return err
	}
	return s.publisher.PublishRoadMatched(ctx, domain.RoadMatched{
		EventID:          s.ids.New("evt"),
		OccurredAt:       s.clock.Now().UTC(),
		TenantID:         update.TenantID,
		VehicleID:        update.VehicleID,
		DeviceID:         update.DeviceID,
		FixTimeRFC:       update.FixTimeRFC,
		Latitude:         match.Latitude,
		Longitude:        match.Longitude,
		SpeedKPH:         *update.SpeedKPH,
		RoadClass:        match.RoadClass,
		RoadName:         match.RoadName,
		OSMSpeedLimitKPH: match.OSMSpeedLimitKPH,
		WayID:            match.WayID,
	})
}

// recentTrace is the vehicle's last few fixes, oldest first, ending at this
// one. Older fixes are dropped: a gap of minutes is a different drive.
func (s *RoadMatchService) recentTrace(update domain.LocationUpdate) ([]LatestLocation, error) {
	history, err := s.history.History(update.TenantID, update.VehicleID, s.cfg.TracePoints)
	if err != nil {
		return nil, err
	}
	oldest := update.OccurredAt.Add(-s.cfg.MaxTraceAge)
	trace := make([]LatestLocation, 0, len(history))
	for _, entry := range history {
		if entry.EventID != update.EventID && !entry.ObservedAt.Before(oldest) && !entry.ObservedAt.After(update.OccurredAt) {
			trace = append(trace, entry)
		}
	}
	slices.Reverse(trace)
	return append(trace, LatestLocation{
		Latitude:   update.Latitude,
		Longitude:  update.Longitude,
		FixTimeRFC: update.FixTimeRFC,
		ObservedAt: update.OccurredAt,
		EventID:    update.EventID,
	}), nil
}
