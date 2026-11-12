package trackinghttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTeltonikaAdapter_FixTimeIsRFC3339(t *testing.T) {
	body := `{"imei":"359779090012345","timestamp":1700000000,"lat":6.5,"lng":3.4,"speed":40,"angle":90}`
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest/vendor/teltonika", strings.NewReader(body))

	cmd, err := teltonikaAdapter{}.decode(req)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if cmd.IMEI != "359779090012345" {
		t.Fatalf("expected IMEI set, got %q", cmd.IMEI)
	}
	if _, err := time.Parse(time.RFC3339, cmd.FixTime); err != nil {
		t.Fatalf("expected RFC3339 fix time, got %q (%v)", cmd.FixTime, err)
	}
}

func TestTraccarAdapter_KnotsToKMHAndIMEI(t *testing.T) {
	body := `{"position":{"id":987654,"protocol":"teltonika","latitude":6.5,"longitude":3.4,"speed":10,"course":90,"fixTime":"2026-02-02T11:00:00Z"},"device":{"uniqueId":"359779090012345"}}`
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest/vendor/traccar", strings.NewReader(body))

	cmd, err := traccarAdapter{}.decode(req)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if cmd.IMEI != "359779090012345" {
		t.Fatalf("expected IMEI from uniqueId, got %q", cmd.IMEI)
	}
	if cmd.SpeedKPH == nil || *cmd.SpeedKPH < 18.5 || *cmd.SpeedKPH > 18.53 {
		t.Fatalf("expected ~18.52 km/h from 10 knots, got %v", cmd.SpeedKPH)
	}
	if cmd.Protocol != "teltonika" {
		t.Fatalf("expected protocol teltonika from traccar payload, got %q", cmd.Protocol)
	}
	if cmd.VendorPositionID != "987654" {
		t.Fatalf("expected vendor position id 987654 from traccar payload, got %q", cmd.VendorPositionID)
	}
}

func TestTraccarAdapter_MissingPositionIDLeavesVendorPositionIDEmpty(t *testing.T) {
	body := `{"position":{"protocol":"teltonika","latitude":6.5,"longitude":3.4,"speed":10,"course":90,"fixTime":"2026-02-02T11:00:00Z"},"device":{"uniqueId":"359779090012345"}}`
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest/vendor/traccar", strings.NewReader(body))

	cmd, err := traccarAdapter{}.decode(req)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if cmd.VendorPositionID != "" {
		t.Fatalf("expected empty vendor position id when traccar payload omits it, got %q", cmd.VendorPositionID)
	}
}

// Captured from Traccar 6.14's json forwarder. Traccar forwards before the
// position is stored, so its id is still 0.
const traccarForwardPayload = `{"position":{"id":0,"attributes":{"batteryLevel":77.0,"distance":0.0,"totalDistance":0.0,"motion":true},"deviceId":1,"protocol":"osmand","serverTime":"2026-09-23T14:33:42.603+00:00","deviceTime":"2026-09-23T14:40:00.000+00:00","fixTime":"2026-09-23T14:40:00.000+00:00","valid":true,"latitude":6.5,"longitude":3.4,"altitude":0.0,"speed":10.0,"course":90.0,"address":null,"accuracy":0.0,"network":null,"geofenceIds":null},"device":{"id":1,"attributes":{},"groupId":0,"calendarId":0,"name":"truck","uniqueId":"359779090012345","status":"online","lastUpdate":"2026-09-23T14:33:42.634+00:00","positionId":1,"phone":null,"model":null,"contact":null,"category":null,"disabled":false,"expirationTime":null}}`

func TestTraccarAdapter_DecodesTraccarForward(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest/vendor/traccar", strings.NewReader(traccarForwardPayload))

	cmd, err := traccarAdapter{}.decode(req)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if cmd.IMEI != "359779090012345" || cmd.Protocol != "osmand" {
		t.Fatalf("expected device and protocol from the forward, got %q/%q", cmd.IMEI, cmd.Protocol)
	}
	if cmd.BatteryLevel == nil || *cmd.BatteryLevel != 77 {
		t.Fatalf("expected battery level 77, got %v", cmd.BatteryLevel)
	}
	if _, err := time.Parse(time.RFC3339, cmd.FixTime); err != nil {
		t.Fatalf("expected an RFC3339 fix time, got %q (%v)", cmd.FixTime, err)
	}
	if cmd.VendorPositionID != "" {
		t.Fatalf("expected no vendor position id for an unsaved position, got %q", cmd.VendorPositionID)
	}
}

func TestTraccarAdapter_GT06Attributes(t *testing.T) {
	body := `{"position":{"protocol":"gt06","valid":false,"latitude":6.5,"longitude":3.4,"speed":10,"fixTime":"2026-11-12T10:00:00.000+00:00","attributes":{"battery":4.1,"power":12.6,"odometer":15000,"iccid":"8923401234567890123","alarm":"door","ignition":true}},"device":{"uniqueId":"359779090012345"}}`
	cmd, err := traccarAdapter{}.decode(httptest.NewRequest(http.MethodPost, "/tracking/ingest/vendor/traccar", strings.NewReader(body)))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if cmd.Protocol != "gt06" || cmd.IMEI != "359779090012345" {
		t.Fatalf("expected gt06 device, got %q/%q", cmd.Protocol, cmd.IMEI)
	}
	if cmd.BatteryLevel != nil {
		t.Fatalf("battery voltage must not become a level, got %v", *cmd.BatteryLevel)
	}
	if cmd.EngineOn == nil || !*cmd.EngineOn {
		t.Fatal("expected ignition on")
	}
	if cmd.BatteryVoltage == nil || *cmd.BatteryVoltage != 4.1 || cmd.PowerVoltage == nil || *cmd.PowerVoltage != 12.6 {
		t.Fatalf("expected battery 4.1V and power 12.6V, got %v/%v", cmd.BatteryVoltage, cmd.PowerVoltage)
	}
	if cmd.OdometerM == nil || *cmd.OdometerM != 15000 || cmd.ICCID != "8923401234567890123" || cmd.Alarm != "door" {
		t.Fatalf("expected odometer/iccid/alarm, got %v/%q/%q", cmd.OdometerM, cmd.ICCID, cmd.Alarm)
	}
	if !cmd.NoFix {
		t.Fatal("valid:false must mark the report as no fix")
	}
}

func TestGenericAdapter_SetsProtocol(t *testing.T) {
	body := `{"imei":"359779090012345","lat":6.5,"lng":3.4}`
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest", strings.NewReader(body))

	cmd, err := genericAdapter{}.decode(req)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if cmd.Protocol != "generic" {
		t.Fatalf("expected protocol generic, got %q", cmd.Protocol)
	}
}

func TestGenericAdapter_IgnoresBodyIdentityAndRequiresIMEI(t *testing.T) {
	body := `{"imei":" 359779090012345 ","tenant_id":"evil","vehicle_id":"v1","device_id":"d1","lat":6.5,"lng":3.4}`
	cmd, err := genericAdapter{}.decode(httptest.NewRequest(http.MethodPost, "/tracking/ingest", strings.NewReader(body)))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if cmd.TenantID != "" || cmd.VehicleID != "" || cmd.DeviceID != "" || cmd.IMEI != "359779090012345" {
		t.Fatalf("body identity must be dropped, got %+v", cmd)
	}

	noIMEI := `{"tenant_id":"t1","vehicle_id":"v1","device_id":"d1","lat":6.5,"lng":3.4}`
	if _, err := (genericAdapter{}).decode(httptest.NewRequest(http.MethodPost, "/tracking/ingest", strings.NewReader(noIMEI))); err == nil {
		t.Fatal("expected error without imei")
	}
}
