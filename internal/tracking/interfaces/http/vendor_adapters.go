package trackinghttp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

// vendorAdapter normalises a vendor-specific payload to IngestLocationCommand.
type vendorAdapter interface {
	decode(r *http.Request) (application.IngestLocationCommand, error)
}

// genericAdapter — our standard normalised format (pass-through).
type genericAdapter struct{}

func (genericAdapter) decode(r *http.Request) (application.IngestLocationCommand, error) {
	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return application.IngestLocationCommand{}, fmt.Errorf("invalid body: %w", err)
	}
	return application.IngestLocationCommand{
		TenantID:  req.TenantID,
		VehicleID: req.VehicleID,
		DeviceID:  req.DeviceID,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		SpeedKPH:  req.SpeedKPH,
		Heading:   req.Heading,
		AccuracyM: req.AccuracyM,
		FixTime:   req.FixTime,
	}, nil
}

// teltonikaPayload — Teltonika FMB series AVL data format.
type teltonikaPayload struct {
	IMEI      string  `json:"imei"`
	Timestamp int64   `json:"timestamp"` // Unix seconds
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Speed     float64 `json:"speed"` // km/h
	Angle     float64 `json:"angle"` // degrees
	Altitude  float64 `json:"altitude"`
	TenantID  string  `json:"tenant_id"`
}

type teltonikaAdapter struct{}

func (teltonikaAdapter) decode(r *http.Request) (application.IngestLocationCommand, error) {
	var p teltonikaPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return application.IngestLocationCommand{}, fmt.Errorf("invalid teltonika payload: %w", err)
	}
	speed := p.Speed
	heading := p.Angle
	var fixTime string
	if p.Timestamp > 0 {
		// Format as RFC3339 for the use case
		fixTime = fmt.Sprintf("%d", p.Timestamp)
	}
	return application.IngestLocationCommand{
		TenantID:  p.TenantID,
		VehicleID: p.IMEI,
		DeviceID:  p.IMEI,
		Latitude:  p.Lat,
		Longitude: p.Lng,
		SpeedKPH:  &speed,
		Heading:   &heading,
		FixTime:   fixTime,
	}, nil
}

// queclinkPayload — Queclink GV series format.
type queclinkPayload struct {
	UniqueID string  `json:"unique_id"`
	Lat      float64 `json:"latitude"`
	Lng      float64 `json:"longitude"`
	SpeedKMH float64 `json:"speed_kmh"`
	Course   float64 `json:"course"`
	Accuracy float64 `json:"accuracy_m"`
	FixTime  string  `json:"fix_time"`
	TenantID string  `json:"tenant_id"`
}

type queclinkAdapter struct{}

func (queclinkAdapter) decode(r *http.Request) (application.IngestLocationCommand, error) {
	var p queclinkPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return application.IngestLocationCommand{}, fmt.Errorf("invalid queclink payload: %w", err)
	}
	speed := p.SpeedKMH
	heading := p.Course
	accuracy := p.Accuracy
	return application.IngestLocationCommand{
		TenantID:  p.TenantID,
		VehicleID: p.UniqueID,
		DeviceID:  p.UniqueID,
		Latitude:  p.Lat,
		Longitude: p.Lng,
		SpeedKPH:  &speed,
		Heading:   &heading,
		AccuracyM: &accuracy,
		FixTime:   p.FixTime,
	}, nil
}

func resolveAdapter(vendor string) (vendorAdapter, bool) {
	switch vendor {
	case "generic", "":
		return genericAdapter{}, true
	case "teltonika":
		return teltonikaAdapter{}, true
	case "queclink":
		return queclinkAdapter{}, true
	default:
		return nil, false
	}
}
