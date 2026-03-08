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
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	New(prefix string) string
}

type LatestLocation struct {
	TenantID   string
	VehicleID  string
	DeviceID   string
	Latitude   float64
	Longitude  float64
	SpeedKPH   *float64
	Heading    *float64
	AccuracyM  *float64
	FixTimeRFC string
	ObservedAt time.Time
}
