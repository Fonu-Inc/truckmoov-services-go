package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

// Statuses that put a job on the live map. A created job has nobody driving it
// and a delivered or cancelled one is over.
var activeMapStatuses = map[domain.JobStatus]bool{
	domain.StatusAssigned: true,
	domain.StatusEnRoute:  true,
	domain.StatusArrived:  true,
	domain.StatusLoaded:   true,
}

type RoutePoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// RouteShaper turns an ordered list of points into an encoded route geometry.
type RouteShaper interface {
	RouteShape(ctx context.Context, points []RoutePoint) (shape string, encoding string, err error)
}

type ActiveJobWaypoint struct {
	Sequence     int      `json:"sequence"`
	Type         string   `json:"type"`
	Address      string   `json:"address"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
	ContactName  string   `json:"contact_name,omitempty"`
	ContactPhone string   `json:"contact_phone,omitempty"`
}

type ActiveJobMapEntry struct {
	JobID             string              `json:"job_id"`
	Reference         string              `json:"reference,omitempty"`
	Status            string              `json:"status"`
	AssignedDriverID  string              `json:"assigned_driver_id,omitempty"`
	AssignedVehicleID string              `json:"assigned_vehicle_id,omitempty"`
	AmountNgn         float64             `json:"amount_ngn,omitempty"`
	Waypoints         []ActiveJobWaypoint `json:"waypoints"`
	RouteShape        string              `json:"route_shape,omitempty"`
	RouteEncoding     string              `json:"route_encoding,omitempty"`
}

type ActiveJobsMapCounts struct {
	ActiveDeliveries int `json:"active_deliveries"`
	Stops            int `json:"stops"`
	Pickups          int `json:"pickups"`
	Dropoffs         int `json:"dropoffs"`
}

type ActiveJobsMap struct {
	Jobs   []ActiveJobMapEntry `json:"jobs"`
	Counts ActiveJobsMapCounts `json:"counts"`
}

type shapeCacheEntry struct {
	shape     string
	encoding  string
	expiresAt time.Time
}

// ActiveJobsMapUseCase assembles the live delivery map. Route geometry is
// cached by the waypoints it was built from, so a dashboard polling every few
// seconds costs one Valhalla call per job per TTL rather than one per request.
type ActiveJobsMapUseCase struct {
	repository    JobRepository
	shaper        RouteShaper
	clock         Clock
	defaultTenant string
	cacheTTL      time.Duration

	mu    sync.Mutex
	cache map[string]shapeCacheEntry
}

func NewActiveJobsMapUseCase(
	repository JobRepository,
	clock Clock,
	defaultTenant string,
	cacheTTL time.Duration,
	shaper RouteShaper,
) *ActiveJobsMapUseCase {
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	return &ActiveJobsMapUseCase{
		repository:    repository,
		shaper:        shaper,
		clock:         clock,
		defaultTenant: defaultTenant,
		cacheTTL:      cacheTTL,
		cache:         map[string]shapeCacheEntry{},
	}
}

func (u *ActiveJobsMapUseCase) Execute(ctx context.Context, tenantID string, scope *JobScope) (ActiveJobsMap, error) {
	resolvedTenant := strings.TrimSpace(tenantID)
	if resolvedTenant == "" {
		resolvedTenant = u.defaultTenant
	}
	if resolvedTenant == "" {
		return ActiveJobsMap{}, fmt.Errorf("tenant id is required")
	}

	jobs := u.repository.FindByTenant(resolvedTenant)

	result := ActiveJobsMap{Jobs: []ActiveJobMapEntry{}}
	for _, job := range jobs {
		if !activeMapStatuses[job.Status] || !scope.Allows(job) {
			continue
		}

		waypoints := waypointsFor(job)
		entry := ActiveJobMapEntry{
			JobID:             job.ID,
			Reference:         job.Reference,
			Status:            string(job.Status),
			AssignedDriverID:  job.AssignedDriverID,
			AssignedVehicleID: job.AssignedVehicleID,
			AmountNgn:         job.AmountNgn,
			Waypoints:         waypoints,
		}
		entry.RouteShape, entry.RouteEncoding = u.shapeFor(ctx, waypoints)

		result.Jobs = append(result.Jobs, entry)
		result.Counts.ActiveDeliveries++
		for _, waypoint := range waypoints {
			result.Counts.Stops++
			switch waypoint.Type {
			case "pickup":
				result.Counts.Pickups++
			case "dropoff":
				result.Counts.Dropoffs++
			}
		}
	}

	return result, nil
}

func waypointsFor(job domain.Job) []ActiveJobWaypoint {
	stops := make([]ActiveJobWaypoint, 0, len(job.Stops)+2)
	stops = append(stops, toWaypoint(job.Pickup, "pickup", len(stops)))
	for _, stop := range job.Stops {
		stops = append(stops, toWaypoint(stop, "stop", len(stops)))
	}
	stops = append(stops, toWaypoint(job.Dropoff, "dropoff", len(stops)))
	return stops
}

func toWaypoint(stop domain.JobStop, stopType string, sequence int) ActiveJobWaypoint {
	return ActiveJobWaypoint{
		Sequence:     sequence,
		Type:         stopType,
		Address:      stop.Address,
		Latitude:     stop.Latitude,
		Longitude:    stop.Longitude,
		ContactName:  stop.ContactName,
		ContactPhone: stop.ContactPhone,
	}
}

// shapeFor never fails the request: a map without geometry still plots its
// pins, so a Valhalla outage degrades the route line rather than the page.
func (u *ActiveJobsMapUseCase) shapeFor(ctx context.Context, waypoints []ActiveJobWaypoint) (string, string) {
	if u.shaper == nil {
		return "", ""
	}

	points := make([]RoutePoint, 0, len(waypoints))
	for _, waypoint := range waypoints {
		if waypoint.Latitude == nil || waypoint.Longitude == nil {
			continue
		}
		points = append(points, RoutePoint{Latitude: *waypoint.Latitude, Longitude: *waypoint.Longitude})
	}
	if len(points) < 2 {
		return "", ""
	}

	key := shapeCacheKey(points)
	now := u.now()
	if cached, ok := u.readCache(key, now); ok {
		return cached.shape, cached.encoding
	}

	shape, encoding, err := u.shaper.RouteShape(ctx, points)
	if err != nil {
		return "", ""
	}

	u.writeCache(key, shapeCacheEntry{shape: shape, encoding: encoding, expiresAt: now.Add(u.cacheTTL)})
	return shape, encoding
}

func (u *ActiveJobsMapUseCase) readCache(key string, now time.Time) (shapeCacheEntry, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	entry, ok := u.cache[key]
	if !ok || entry.expiresAt.Before(now) {
		return shapeCacheEntry{}, false
	}
	return entry, true
}

func (u *ActiveJobsMapUseCase) writeCache(key string, entry shapeCacheEntry) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.cache[key] = entry
}

func (u *ActiveJobsMapUseCase) now() time.Time {
	if u.clock == nil {
		return time.Now().UTC()
	}
	return u.clock.Now().UTC()
}

func shapeCacheKey(points []RoutePoint) string {
	builder := strings.Builder{}
	for _, point := range points {
		builder.WriteString(strconv.FormatFloat(point.Latitude, 'f', 5, 64))
		builder.WriteByte(',')
		builder.WriteString(strconv.FormatFloat(point.Longitude, 'f', 5, 64))
		builder.WriteByte(';')
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}
