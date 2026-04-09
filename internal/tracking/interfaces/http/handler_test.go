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
	ingestUseCase := application.NewIngestLocationUseCase(
		publisher,
		latestRepo,
		clock,
		ids,
		"default",
		historyRepo,
		application.TelemetryConfig{},
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

	return NewHandler(ingestUseCase, alertUseCase, queryUseCase, "", activityPublisher), activityPublisher
}

func TestHandler_IngestContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{"vehicle_id":"vehicle-1","device_id":"device-1","lat":6.5,"lng":3.4}`)
	req := httptest.NewRequest(http.MethodPost, "/tracking/ingest", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["published"] != true {
		t.Fatalf("expected published=true")
	}
}

func TestHandler_WebsocketPlaceholderContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/tracking/ws", nil)
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusNotImplemented {
		t.Fatalf("expected status %d, got %d", http.StatusNotImplemented, res.Code)
	}
}

func TestHandler_ListVehiclesContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	server := httpx.WithAuthContext(mux, "replace-me")

	body := bytes.NewBufferString(`{"tenant_id":"tenant-1","vehicle_id":"vehicle-1","device_id":"device-1","lat":6.5,"lng":3.4}`)
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
	var payload struct {
		Vehicles []map[string]any `json:"vehicles"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload.Vehicles) != 1 {
		t.Fatalf("expected 1 vehicle, got %#v", payload.Vehicles)
	}
	if payload.Vehicles[0]["vehicle_id"] != "vehicle-1" {
		t.Fatalf("unexpected vehicle id %#v", payload.Vehicles[0]["vehicle_id"])
	}
}

func TestHandler_VehicleHistoryContract(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	server := httpx.WithAuthContext(mux, "replace-me")

	lines := []string{
		`{"tenant_id":"tenant-2","vehicle_id":"vehicle-2","device_id":"device-2","lat":6.6,"lng":3.7,"fix_time":"2026-02-02T11:00:00Z"}`,
		`{"tenant_id":"tenant-2","vehicle_id":"vehicle-2","device_id":"device-2","lat":6.7,"lng":3.8,"fix_time":"2026-02-02T11:05:00Z"}`,
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

	historyReq := httptest.NewRequest(http.MethodGet, "/tracking/vehicles/vehicle-2/history?limit=1", nil)
	historyReq.Header.Set("Authorization", "Bearer "+makeTrackingBearerToken("replace-me", "tenant-2", "DISPATCHER"))
	historyRes := httptest.NewRecorder()
	server.ServeHTTP(historyRes, historyReq)
	if historyRes.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, historyRes.Code)
	}

	var historyPayload struct {
		VehicleID string           `json:"vehicle_id"`
		History   []map[string]any `json:"history"`
	}
	if err := json.Unmarshal(historyRes.Body.Bytes(), &historyPayload); err != nil {
		t.Fatalf("failed to decode history response: %v", err)
	}
	if historyPayload.VehicleID != "vehicle-2" {
		t.Fatalf("expected vehicle-2 returned, got %s", historyPayload.VehicleID)
	}
	if len(historyPayload.History) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(historyPayload.History))
	}
	if historyPayload.History[0]["fix_time"] != "2026-02-02T11:05:00Z" {
		t.Fatalf("expected most recent fix_time, got %#v", historyPayload.History[0]["fix_time"])
	}
}

func TestHandler_ListVehiclesRejectsTenantQuery(t *testing.T) {
	handler, _ := buildTrackingTestHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	server := httpx.WithAuthContext(mux, "replace-me")

	body := bytes.NewBufferString(`{"tenant_id":"tenant-1","vehicle_id":"vehicle-1","device_id":"device-1","lat":6.5,"lng":3.4}`)
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
	handler.RegisterRoutes(mux)
	server := httpx.WithAuthContext(mux, "replace-me")

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
