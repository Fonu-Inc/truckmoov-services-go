package domain

import (
	"fmt"
	"time"
)

// RoadMatched says which road a fix was driven on, as map-matched by Valhalla.
// Fleet turns it into road speed and route-deviation alerts; it is published
// apart from the location update so a slow match never delays the live map.
type RoadMatched struct {
	EventID    string
	OccurredAt time.Time
	TenantID   string
	VehicleID  string
	DeviceID   string
	FixTimeRFC string
	// Latitude and Longitude are the fix snapped onto the matched road.
	Latitude  float64
	Longitude float64
	SpeedKPH  float64
	RoadClass string
	RoadName  string
	// OSMSpeedLimitKPH is the posted limit when OpenStreetMap has one.
	OSMSpeedLimitKPH *int
	WayID            int64
}

func (e RoadMatched) Subject() string {
	return fmt.Sprintf("tm.%s.vehicle.road.matched", e.TenantID)
}
