package trackinghttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
		Protocol:  "generic",
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
		// Teltonika sends Unix seconds; the ingest pipeline requires RFC3339.
		fixTime = time.Unix(p.Timestamp, 0).UTC().Format(time.RFC3339)
	}
	return application.IngestLocationCommand{
		TenantID:  p.TenantID,
		IMEI:      p.IMEI,
		Latitude:  p.Lat,
		Longitude: p.Lng,
		SpeedKPH:  &speed,
		Heading:   &heading,
		FixTime:   fixTime,
		Protocol:  "teltonika",
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
		IMEI:      p.UniqueID,
		Latitude:  p.Lat,
		Longitude: p.Lng,
		SpeedKPH:  &speed,
		Heading:   &heading,
		AccuracyM: &accuracy,
		FixTime:   p.FixTime,
		Protocol:  "queclink",
	}, nil
}

// traccarPayload — Traccar position-forwarding JSON (forward.json=true). The
// device.uniqueId is the IMEI; speed arrives in knots.
type traccarPayload struct {
	Position struct {
		Protocol   string         `json:"protocol"`
		Latitude   float64        `json:"latitude"`
		Longitude  float64        `json:"longitude"`
		Speed      float64        `json:"speed"` // knots
		Course     float64        `json:"course"`
		DeviceTime string         `json:"deviceTime"`
		FixTime    string         `json:"fixTime"`
		Attributes map[string]any `json:"attributes"`
	} `json:"position"`
	Device struct {
		UniqueID string `json:"uniqueId"`
	} `json:"device"`
}

type traccarAdapter struct{}

func (traccarAdapter) decode(r *http.Request) (application.IngestLocationCommand, error) {
	var p traccarPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return application.IngestLocationCommand{}, fmt.Errorf("invalid traccar payload: %w", err)
	}
	speed := p.Position.Speed * 1.852 // knots → km/h
	heading := p.Position.Course
	fixTime := p.Position.FixTime
	if fixTime == "" {
		fixTime = p.Position.DeviceTime
	}
	attrs := p.Position.Attributes
	// Tenant is intentionally empty: it is resolved from the IMEI identity cache,
	// since Traccar's forward payload carries no tenant.
	return application.IngestLocationCommand{
		IMEI:            p.Device.UniqueID,
		Latitude:        p.Position.Latitude,
		Longitude:       p.Position.Longitude,
		SpeedKPH:        &speed,
		Heading:         &heading,
		BatteryLevel:    attrFloat(attrs, "batteryLevel", "battery"),
		SignalStrength:  attrFloat(attrs, "rssi", "signal"),
		FuelLevel:       attrFloat(attrs, "fuel", "fuelLevel"),
		EngineOn:        attrBool(attrs, "ignition"),
		FirmwareVersion: attrString(attrs, "firmware", "version"),
		FixTime:         fixTime,
		Protocol:        p.Position.Protocol,
	}, nil
}

func attrFloat(m map[string]any, keys ...string) *float64 {
	for _, k := range keys {
		switch n := m[k].(type) {
		case float64:
			return &n
		case int:
			f := float64(n)
			return &f
		}
	}
	return nil
}

func attrBool(m map[string]any, keys ...string) *bool {
	for _, k := range keys {
		if b, ok := m[k].(bool); ok {
			return &b
		}
	}
	return nil
}

func attrString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func resolveAdapter(vendor string) (vendorAdapter, bool) {
	switch vendor {
	case "generic", "":
		return genericAdapter{}, true
	case "teltonika":
		return teltonikaAdapter{}, true
	case "traccar":
		return traccarAdapter{}, true
	case "queclink":
		return queclinkAdapter{}, true
	default:
		return nil, false
	}
}
