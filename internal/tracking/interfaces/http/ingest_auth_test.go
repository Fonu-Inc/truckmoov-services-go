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
)

const unknownImeiBody = `{"position":{"id":0,"protocol":"osmand","latitude":6.5,"longitude":3.4,"speed":10,"course":90,"fixTime":"2026-02-02T11:00:00Z"},"device":{"uniqueId":"359779090077777"}}`

type ingestFixture struct {
	mux        *http.ServeMux
	latest     *infrastructure.InMemoryLatestLocationRepository
	publisher  *fakeTrackingPublisher
	quarantine *application.Quarantine
}

func newIngestFixture(apiKey string, open bool) ingestFixture {
	clock := fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)}
	latest := infrastructure.NewInMemoryLatestLocationRepository()
	history := infrastructure.NewInMemoryLocationHistoryRepository()
	publisher := &fakeTrackingPublisher{}
	quarantine := application.NewQuarantine(infrastructure.NewInMemoryQuarantineRepository(), clock)
	ingest := application.NewIngestLocationUseCase(publisher, latest, clock, fixedTrackingID{value: "1"},
		"default", history, application.TelemetryConfig{}, infrastructure.NewInMemoryIMEIResolver(), nil).WithQuarantine(quarantine)
	handler := NewHandler(ingest, nil, application.NewTrackingQueryUseCase(latest, history, "default"), nil, apiKey, nil, nil).
		WithOpenIngest(open).WithQuarantine(quarantine)
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	return ingestFixture{mux: mux, latest: latest, publisher: publisher, quarantine: quarantine}
}

func (f ingestFixture) post(key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest/vendor/traccar", strings.NewReader(unknownImeiBody))
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	res := httptest.NewRecorder()
	f.mux.ServeHTTP(res, req)
	return res
}

func TestIngest_RefusedWhenKeyUnsetAndNotOpened(t *testing.T) {
	f := newIngestFixture("", false)
	if res := f.post(""); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", res.Code)
	}
}

func TestIngest_OpenOnlyWhenExplicitlyAllowed(t *testing.T) {
	f := newIngestFixture("", true)
	if res := f.post(""); res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}
}

func TestIngest_KeyIsEnforced(t *testing.T) {
	f := newIngestFixture("secret-key", false)
	if res := f.post("wrong"); res.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key expected 401, got %d", res.Code)
	}
	if res := f.post(""); res.Code != http.StatusUnauthorized {
		t.Fatalf("missing key expected 401, got %d", res.Code)
	}
	if res := f.post("secret-key"); res.Code != http.StatusAccepted {
		t.Fatalf("right key expected 202, got %d", res.Code)
	}
}

func TestIngest_UnknownImeiIsQuarantinedAndListed(t *testing.T) {
	f := newIngestFixture("k", false)
	res := f.post("k")
	var payload map[string]any
	decodeData(t, res.Body.Bytes(), &payload)
	if payload["skipped"] != true || payload["published"] == true {
		t.Fatalf("expected quarantine skip, got %v", payload)
	}
	if stored, _ := f.latest.List("default"); len(stored) != 0 {
		t.Fatalf("unknown device must not be stored")
	}
	devices, _ := f.quarantine.List(t.Context())
	if len(devices) != 1 || devices[0].IMEI != "359779090077777" {
		t.Fatalf("expected the IMEI in quarantine, got %+v", devices)
	}
}
