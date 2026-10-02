package trackingws

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/truckmoov/services-go/internal/tracking/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

type Bridge struct {
	ingest     *application.IngestLocationUseCase
	alert      *application.PublishAlertUseCase
	userTokens *identity.UserTokenVerifier
}

func NewBridge(
	ingest *application.IngestLocationUseCase,
	alert *application.PublishAlertUseCase,
	userTokens *identity.UserTokenVerifier,
) *Bridge {
	return &Bridge{ingest: ingest, alert: alert, userTokens: userTokens}
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	claims, ok := httpx.ParseToken(token, b.userTokens)
	if !ok || claims.TenantID != identity.SystemTenantID || (claims.Role != "SUPER_ADMIN" && claims.Role != "SYSTEM_ADMIN") {
		http.NotFound(w, r)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// The http.Server's ReadTimeout/WriteTimeout deadlines are inherited by the
	// hijacked connection; clear them so this long-lived WebSocket is not torn
	// down after those windows elapse.
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg InboundMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			b.writeError(conn, "invalid message format")
			continue
		}

		if msg.Topic == "ping" {
			b.write(conn, OutboundMessage{Type: "pingresp", Timestamp: now()})
			continue
		}

		pt, ok := parseTopic(msg.Topic)
		if !ok {
			b.writeError(conn, "invalid topic: "+msg.Topic)
			continue
		}

		b.dispatch(r.Context(), conn, pt, msg)
	}
}

func (b *Bridge) dispatch(ctx context.Context, conn *websocket.Conn, pt parsedTopic, msg InboundMessage) {
	switch pt.action {
	case "connect":
		b.write(conn, OutboundMessage{
			Type:      "connack",
			DeviceID:  pt.imei,
			Timestamp: now(),
		})

	case "telemetry":
		var p TelemetryPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			b.writeError(conn, "invalid telemetry payload")
			return
		}
		speed := p.Speed
		heading := p.Heading
		result, err := b.ingest.Execute(ctx, application.IngestLocationCommand{
			IMEI:      pt.imei,
			Latitude:  p.Latitude,
			Longitude: p.Longitude,
			SpeedKPH:  &speed,
			Heading:   &heading,
			Protocol:  "ws",
		})
		if err != nil {
			b.writeError(conn, "telemetry not accepted, retry")
			return
		}
		b.write(conn, OutboundMessage{
			Type:       "puback",
			Topic:      msg.Topic,
			EventCount: publishedCount(result),
			Message:    result.SkipReason,
			Timestamp:  now(),
		})

	case "alert":
		var p AlertPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			b.writeError(conn, "invalid alert payload")
			return
		}
		device, err := b.ingest.ResolveDevice(pt.imei)
		if err != nil {
			b.writeError(conn, "device lookup unavailable, retry")
			return
		}
		if device == nil {
			b.writeError(conn, "unknown device: "+pt.imei)
			return
		}
		_, _ = b.alert.Execute(ctx, application.PublishAlertCommand{
			TenantID:  device.TenantID,
			AlertType: p.Type,
			Severity:  p.Severity,
			VehicleID: firstNonEmpty(device.VehicleUUID, device.DeviceID),
			Details: map[string]any{
				"latitude":      p.Latitude,
				"longitude":     p.Longitude,
				"geofence_id":   p.GeofenceID,
				"geofence_name": p.GeofenceName,
			},
		})
		b.write(conn, OutboundMessage{
			Type:       "puback",
			Topic:      msg.Topic,
			EventCount: 1,
			Timestamp:  now(),
		})

	case "status":
		b.write(conn, OutboundMessage{
			Type:      "puback",
			Topic:     msg.Topic,
			Timestamp: now(),
		})

	default:
		b.writeError(conn, "unknown action: "+pt.action)
	}
}

func publishedCount(result application.IngestLocationResult) int {
	if result.Published {
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (b *Bridge) write(conn *websocket.Conn, msg OutboundMessage) {
	data, _ := json.Marshal(msg)
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

func (b *Bridge) writeError(conn *websocket.Conn, message string) {
	b.write(conn, OutboundMessage{Type: "error", Message: message, Timestamp: now()})
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
