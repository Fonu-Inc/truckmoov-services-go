package trackinghttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/internal/tracking/infrastructure"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/logger"
)

func TestHandler_SuspendedMessagesAreDroppedOnEveryHTTPPath(t *testing.T) {
	const imei = "359779090012345"
	tests := []struct {
		name string
		path string
		body string
	}{
		{"generic", "/tracking/ingest", `{"tenant_id":"t-bad","vehicle_id":"v","device_id":"d","lat":6.5,"lng":3.4}`},
		{"teltonika", "/tracking/ingest/vendor/teltonika", `{"imei":"` + imei + `","timestamp":1700000000,"lat":6.5,"lng":3.4,"speed":40,"angle":90}`},
		{"traccar", "/tracking/ingest/vendor/traccar", `{"position":{"id":9,"protocol":"osmand","latitude":6.5,"longitude":3.4,"speed":10,"course":90,"fixTime":"2026-02-02T11:00:00Z"},"device":{"uniqueId":"` + imei + `"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)}
			latest := infrastructure.NewInMemoryLatestLocationRepository()
			history := infrastructure.NewInMemoryLocationHistoryRepository()
			telemetry := infrastructure.NewInMemoryTelemetryRepository()
			resolver := infrastructure.NewInMemoryIMEIResolver()
			gate := application.NewIngestGate(application.GateConfig{}, nil, nil, clock, logger.New("test"))
			gate.Replace(application.SuspensionSnapshot{
				Tenants: map[string]struct{}{"t-bad": {}},
				IMEIs:   map[string]struct{}{imei: {}},
			}, clock.now)
			ingest := application.NewIngestLocationUseCase(&fakeTrackingPublisher{}, latest, clock, fixedTrackingID{value: "1"},
				"default", history, application.TelemetryConfig{}, resolver, telemetry).WithGate(gate)
			handler := NewHandler(ingest, nil, application.NewTrackingQueryUseCase(latest, history, "default"), nil, "", nil, &fakeTrackingActivityPublisher{}).WithOpenIngest(true)
			mux := http.NewServeMux()
			handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)

			if res.Code != http.StatusAccepted {
				t.Fatalf("status %d: %s", res.Code, res.Body.String())
			}
			var payload map[string]any
			decodeData(t, res.Body.Bytes(), &payload)
			if payload["skipped"] != true || payload["published"] == true {
				t.Fatalf("expected dropped, got %v", payload)
			}
			if reason, _ := payload["skip_reason"].(string); reason != "tenant_suspended" && reason != "device_suspended" {
				t.Fatalf("unexpected reason %v", payload["skip_reason"])
			}
			for _, tenant := range []string{"t-bad", "default"} {
				if stored, _ := latest.List(tenant); len(stored) != 0 {
					t.Fatalf("nothing may be stored for %s", tenant)
				}
			}
		})
	}
}
