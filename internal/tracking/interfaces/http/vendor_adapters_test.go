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
	body := `{"position":{"latitude":6.5,"longitude":3.4,"speed":10,"course":90,"fixTime":"2026-02-02T11:00:00Z"},"device":{"uniqueId":"359779090012345"}}`
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
}
