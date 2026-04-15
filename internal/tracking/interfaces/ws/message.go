package trackingws

import (
	"encoding/json"
	"strings"
)

type InboundMessage struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

type ConnectPayload struct {
	TenantID        string  `json:"tenant_id"`
	Manufacturer    string  `json:"manufacturer"`
	Model           string  `json:"model"`
	FirmwareVersion string  `json:"firmware_version"`
	SignalStrength  float64 `json:"signal_strength"`
	BatteryVoltage  float64 `json:"battery_voltage"`
}

type TelemetryPayload struct {
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	Speed          float64 `json:"speed"`
	Heading        float64 `json:"heading"`
	Altitude       float64 `json:"altitude"`
	FuelLevel      float64 `json:"fuel_level"`
	BatteryVoltage float64 `json:"battery_voltage"`
	EngineStatus   string  `json:"engine_status"`
	SignalStrength  float64 `json:"signal_strength"`
	Satellites     int     `json:"satellites"`
}

type AlertPayload struct {
	Type          string  `json:"type"`
	Severity      string  `json:"severity"`
	Latitude      float64 `json:"latitude"`
	Longitude     float64 `json:"longitude"`
	GeofenceID    string  `json:"geofence_id"`
	GeofenceName  string  `json:"geofence_name"`
}

type OutboundMessage struct {
	Type       string `json:"type"`
	Topic      string `json:"topic,omitempty"`
	DeviceID   string `json:"deviceId,omitempty"`
	EventCount int    `json:"eventCount,omitempty"`
	Message    string `json:"message,omitempty"`
	Timestamp  string `json:"timestamp"`
}

type parsedTopic struct {
	imei   string
	action string
}

func parseTopic(topic string) (parsedTopic, bool) {
	parts := strings.Split(topic, "/")
	if len(parts) != 3 || parts[0] != "device" {
		return parsedTopic{}, false
	}
	return parsedTopic{imei: parts[1], action: parts[2]}, true
}
