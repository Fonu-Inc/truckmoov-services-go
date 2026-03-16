package application

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/domain"
)

type TrackingEventPublisher interface {
	PublishLocationUpdate(ctx context.Context, event domain.LocationUpdate) error
	PublishAlertTriggered(ctx context.Context, event domain.AlertTriggered) error
}

type LatestLocationRepository interface {
	Save(location LatestLocation) error
	Get(tenantID, vehicleID string) (*LatestLocation, error)
}

type LatestLocationReader interface {
	List(tenantID string) ([]LatestLocation, error)
}

type LocationHistoryRepository interface {
	Append(entry LatestLocation) error
	History(tenantID, vehicleID string, limit int) ([]LatestLocation, error)
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	New(prefix string) string
}

type LatestLocation struct {
	TenantID   string    `json:"tenant_id"`
	VehicleID  string    `json:"vehicle_id"`
	DeviceID   string    `json:"device_id"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	SpeedKPH   *float64  `json:"speed_kph,omitempty"`
	Heading    *float64  `json:"heading,omitempty"`
	AccuracyM  *float64  `json:"accuracy_m,omitempty"`
	FixTimeRFC string    `json:"fix_time"`
	ObservedAt time.Time `json:"observed_at"`
	EventID    string    `json:"event_id"`
}
