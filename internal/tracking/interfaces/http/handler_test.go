package trackinghttp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
	"github.com/truckmoov/services-go/internal/tracking/domain"
	"github.com/truckmoov/services-go/internal/tracking/infrastructure"
	"github.com/truckmoov/services-go/pkg/activitylog"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

type fakeTrackingPublisher struct{}

func (f *fakeTrackingPublisher) PublishLocationUpdate(_ context.Context, _ domain.LocationUpdate) error {
	return nil
}

func (f *fakeTrackingPublisher) PublishAlertTriggered(_ context.Context, _ domain.AlertTriggered) error {
	return nil
}

type fakeTrackingActivityPublisher struct {
	commands []activitylog.PublishCommand
}

func (f *fakeTrackingActivityPublisher) Publish(_ context.Context, command activitylog.PublishCommand) error {
	f.commands = append(f.commands, command)
	return nil
}

type fixedTrackingClock struct{ now time.Time }

func (f fixedTrackingClock) Now() time.Time { return f.now }

type fixedTrackingID struct{ value string }

func (f fixedTrackingID) New(prefix string) string { return prefix + "-" + f.value }

func makeTrackingBearerToken(secret, tenantID, role string) string {
	payload := []byte(`{"sub":"user-1","email":"tracking@example.com","role":"` + role + `","tenantId":"` + tenantID + `","exp":` + fmt.Sprint(time.Now().Add(time.Hour).Unix()) + `}`)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature
}

func buildTrackingTestHandler() (*Handler, *fakeTrackingActivityPublisher) {
	clock := fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)}
	ids := fixedTrackingID{value: "1"}
	publisher := &fakeTrackingPublisher{}
	activityPublisher := &fakeTrackingActivityPublisher{}

	latestRepo := infrastructure.NewInMemoryLatestLocationRepository()
	historyRepo := infrastructure.NewInMemoryLocationHistoryRepository()
	telemetryRepo := infrastructure.NewInMemoryTelemetryRepository()
	imeiResolver := infrastructure.NewInMemoryIMEIResolver()
	for _, n := range []string{"1", "2"} {
		_ = imeiResolver.Upsert(application.DeviceIdentity{IMEI: "imei-" + n, TenantID: "tenant-" + n, VehicleUUID: "vehicle-" + n, DeviceID: "device-" + n})
	}
	ingestUseCase := application.NewIngestLocationUseCase(
		publisher,
		latestRepo,
		clock,
		ids,
		"default",
		historyRepo,
		application.TelemetryConfig{},
		imeiResolver,
		telemetryRepo,
	)
	alertUseCase := application.NewPublishAlertUseCase(
		publisher,
		clock,
		ids,
		"default",
	)
	queryUseCase := application.NewTrackingQueryUseCase(
		latestRepo,
		historyRepo,
		"default",
	)
	deviceQueryUseCase := application.NewDeviceTelemetryQueryUseCase(telemetryRepo, clock, 600, nil)

	return NewHandler(ingestUseCase, alertUseCase, queryUseCase, deviceQueryUseCase, "", nil, activityPublisher).WithOpenIngest(true), activityPublisher
}

func TestHandler_IngestContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	body := bytes.NewBufferString(`{"imei":"imei-1","lat":6.5,"lng":3.4}`)
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}

	var payload map[string]any
	decodeData(t, res.Body.Bytes(), &payload)
	if payload["published"] != true {
		t.Fatalf("expected published=true")
	}
}

func TestHandler_WebsocketPlaceholderContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/tracking/ws", nil)
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	// Refused exactly as a path that does not exist.
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, res.Code)
	}
}

func TestHandler_ListVehiclesContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))

	body := bytes.NewBufferString(`{"imei":"imei-1","tenant_id":"forged","lat":6.5,"lng":3.4}`)
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/tracking/vehicles", nil)
	listReq.Header.Set("Authorization", "Bearer "+makeTrackingBearerToken("replace-me", "tenant-1", "DISPATCHER"))
	listRes := httptest.NewRecorder()
	server.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, listRes.Code)
	}
	var vehicles []map[string]any
	decodeData(t, listRes.Body.Bytes(), &vehicles)
	if len(vehicles) != 1 {
		t.Fatalf("expected 1 vehicle, got %#v", vehicles)
	}
	if vehicles[0]["vehicle_id"] != "vehicle-1" {
		t.Fatalf("unexpected vehicle id %#v", vehicles[0]["vehicle_id"])
	}
}

func TestHandler_VehicleHistoryContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))

	lines := []string{
		`{"imei":"imei-2","lat":6.6,"lng":3.7,"fix_time":"2026-02-02T11:00:00Z"}`,
		`{"imei":"imei-2","lat":6.7,"lng":3.8,"fix_time":"2026-02-02T11:05:00Z"}`,
	}
	for _, payload := range lines {
		req := httptest.NewRequest(http.MethodPost, "/tracking/ingest", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusAccepted {
			t.Fatalf("ingest expected %d, got %d payload=%s", http.StatusAccepted, res.Code, payload)
		}
	}

	// History append is async off the ingest path; wait briefly for the entry.
	var historyPayload struct {
		VehicleID string           `json:"vehicle_id"`
		History   []map[string]any `json:"history"`
	}
	deadline := time.Now().Add(time.Second)
	for {
		historyReq := httptest.NewRequest(http.MethodGet, "/tracking/vehicles/vehicle-2/history?limit=1", nil)
		historyReq.Header.Set("Authorization", "Bearer "+makeTrackingBearerToken("replace-me", "tenant-2", "DISPATCHER"))
		historyRes := httptest.NewRecorder()
		server.ServeHTTP(historyRes, historyReq)
		if historyRes.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, historyRes.Code)
		}
		decodeData(t, historyRes.Body.Bytes(), &historyPayload)
		if len(historyPayload.History) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 1 history entry within 1s, got %d", len(historyPayload.History))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if historyPayload.VehicleID != "vehicle-2" {
		t.Fatalf("expected vehicle-2 returned, got %s", historyPayload.VehicleID)
	}
	if historyPayload.History[0]["fix_time"] != "2026-02-02T11:05:00Z" {
		t.Fatalf("expected most recent fix_time, got %#v", historyPayload.History[0]["fix_time"])
	}
}

func TestHandler_ListVehiclesRejectsTenantQuery(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))

	body := bytes.NewBufferString(`{"imei":"imei-1","tenant_id":"forged","lat":6.5,"lng":3.4}`)
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/tracking/vehicles?tenant_id=client-tenant", nil)
	listReq.Header.Set("Authorization", "Bearer "+makeTrackingBearerToken("replace-me", "tenant-1", "DISPATCHER"))
	listRes := httptest.NewRecorder()
	server.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, listRes.Code, listRes.Body.String())
	}
}

func TestHandler_AlertPublishesActivity(t *testing.T) {
	handler, activityPublisher := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))

	body := bytes.NewBufferString(`{"tenant_id":"tenant-7","alert_type":"geofence_breach","severity":"high","vehicle_id":"vehicle-7","job_id":"job-7"}`)
	req := httptest.NewRequest(http.MethodPost, "/tracking/alerts", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+makeTrackingBearerToken("replace-me", "tenant-7", "DISPATCHER"))
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusAccepted, res.Code, res.Body.String())
	}
	if len(activityPublisher.commands) != 1 {
		t.Fatalf("expected one activity command, got %d", len(activityPublisher.commands))
	}
	command := activityPublisher.commands[0]
	if command.TenantID != "tenant-7" {
		t.Fatalf("expected tenant-7, got %q", command.TenantID)
	}
	if command.Action != "tracking.alert.published" {
		t.Fatalf("expected tracking.alert.published, got %q", command.Action)
	}
	if command.EntityType != "alert" {
		t.Fatalf("expected entity type alert, got %q", command.EntityType)
	}
	if command.ActorRole != "DISPATCHER" {
		t.Fatalf("expected dispatcher actor role, got %q", command.ActorRole)
	}
}

func TestAlert_RequiresAuth(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))

	body := bytes.NewBufferString(`{"tenant_id":"tenant-7","alert_type":"geofence_breach","severity":"high","vehicle_id":"vehicle-7","job_id":"job-7"}`)
	req := httptest.NewRequest(http.MethodPost, "/tracking/alerts", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusUnauthorized, res.Code, res.Body.String())
	}
}

func TestHandler_DocsContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))

	for _, path := range []string{"/tracking/docs", "/docs/tracking"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("docs path %s expected %d, got %d", path, http.StatusOK, res.Code)
		}
		if !bytes.Contains(res.Body.Bytes(), []byte("SwaggerUIBundle")) {
			t.Fatalf("docs path %s did not return swagger ui html", path)
		}
	}

	for _, path := range []string{"/tracking/docs/openapi.json", "/docs/tracking/openapi.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("openapi path %s expected %d, got %d", path, http.StatusOK, res.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
			t.Fatalf("openapi path %s invalid json: %v", path, err)
		}
		if payload["openapi"] != "3.0.3" {
			t.Fatalf("openapi path %s expected openapi=3.0.3", path)
		}
		paths, ok := payload["paths"].(map[string]any)
		if !ok || paths["/tracking/devices/{deviceId}/telemetry"] == nil {
			t.Fatalf("openapi path %s missing device telemetry path", path)
		}
	}
}

func decodeData(t *testing.T, body []byte, dest any) {
	t.Helper()
	var envelope struct {
		Status bool            `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !envelope.Status {
		t.Fatalf("expected status=true, got %s", body)
	}
	if err := json.Unmarshal(envelope.Data, dest); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}

func TestHandler_VehicleHistoryWithNoPointsIsAnEmptyArray(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))

	req := httptest.NewRequest(http.MethodGet, "/tracking/vehicles/vehicle-none/history", nil)
	req.Header.Set("Authorization", "Bearer "+makeTrackingBearerToken("replace-me", "tenant-1", "DISPATCHER"))
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}

	var payload struct {
		History json.RawMessage `json:"history"`
	}
	decodeData(t, res.Body.Bytes(), &payload)
	if string(payload.History) != "[]" {
		t.Fatalf("expected history [], got %s", payload.History)
	}
}
