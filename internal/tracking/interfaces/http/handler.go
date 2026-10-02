package trackinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/application"
	trackingws "github.com/truckmoov/services-go/internal/tracking/interfaces/ws"
	"github.com/truckmoov/services-go/pkg/activitylog"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

type ingestRequest struct {
	IMEI      string   `json:"imei"`
	TenantID  string   `json:"tenant_id"`
	VehicleID string   `json:"vehicle_id"`
	DeviceID  string   `json:"device_id"`
	Latitude  float64  `json:"lat"`
	Longitude float64  `json:"lng"`
	SpeedKPH  *float64 `json:"speed_kph,omitempty"`
	Heading   *float64 `json:"heading,omitempty"`
	AccuracyM *float64 `json:"accuracy_m,omitempty"`
	FixTime   string   `json:"fix_time,omitempty"`
}

type alertRequest struct {
	TenantID  string `json:"tenant_id"`
	AlertType string `json:"alert_type"`
	Severity  string `json:"severity"`
	VehicleID string `json:"vehicle_id,omitempty"`
	JobID     string `json:"job_id,omitempty"`
	Details   any    `json:"details,omitempty"`
}

type activityPublisher interface {
	Publish(ctx context.Context, command activitylog.PublishCommand) error
}

type Handler struct {
	ingestLocationUseCase       *application.IngestLocationUseCase
	publishAlertUseCase         *application.PublishAlertUseCase
	trackingQueryUseCase        *application.TrackingQueryUseCase
	deviceTelemetryQueryUseCase *application.DeviceTelemetryQueryUseCase
	gpsAPIKey                   string
	activityPublisher           activityPublisher
	wsBridge                    *trackingws.Bridge
	gate                        *application.IngestGate
	rate                        *application.MessageRate
	quarantine                  *application.Quarantine
	allowOpenIngest             bool
	missingIMEI                 atomic.Int64
}

func (h *Handler) WithQuarantine(quarantine *application.Quarantine) *Handler {
	h.quarantine = quarantine
	return h
}

// WithOpenIngest lets ingest run with no GPS_API_KEY. Dev only.
func (h *Handler) WithOpenIngest(allow bool) *Handler {
	h.allowOpenIngest = allow
	return h
}

func (h *Handler) WithGate(gate *application.IngestGate) *Handler {
	h.gate = gate
	return h
}

func (h *Handler) WithMessageRate(rate *application.MessageRate) *Handler {
	h.rate = rate
	return h
}

func NewHandler(
	ingestLocationUseCase *application.IngestLocationUseCase,
	publishAlertUseCase *application.PublishAlertUseCase,
	trackingQueryUseCase *application.TrackingQueryUseCase,
	deviceTelemetryQueryUseCase *application.DeviceTelemetryQueryUseCase,
	gpsAPIKey string,
	userTokens *identity.UserTokenVerifier,
	ap activityPublisher,
) *Handler {
	return &Handler{
		ingestLocationUseCase:       ingestLocationUseCase,
		publishAlertUseCase:         publishAlertUseCase,
		trackingQueryUseCase:        trackingQueryUseCase,
		deviceTelemetryQueryUseCase: deviceTelemetryQueryUseCase,
		gpsAPIKey:                   gpsAPIKey,
		activityPublisher:           ap,
		wsBridge:                    trackingws.NewBridge(ingestLocationUseCase, publishAlertUseCase, userTokens),
	}
}

func (h *Handler) RegisterRoutes(routes *httpx.Routes) {
	routes.HandleFunc("/tracking/health", []httpx.Audience{httpx.Public}, httpx.None(), h.health)
	routes.HandleFunc("/tracking/docs", []httpx.Audience{httpx.Public}, httpx.None(), h.docsUI)
	routes.HandleFunc("/tracking/docs/openapi.json", []httpx.Audience{httpx.Public}, httpx.None(), h.docsOpenAPI)
	routes.HandleFunc("/docs/tracking", []httpx.Audience{httpx.Public}, httpx.None(), h.docsUI)
	routes.HandleFunc("/docs/tracking/openapi.json", []httpx.Audience{httpx.Public}, httpx.None(), h.docsOpenAPI)
	routes.Handle("/tracking/ingest", []httpx.Audience{httpx.Public}, httpx.None(), h.withAPIKey(http.HandlerFunc(h.ingest)))
	routes.Handle("/tracking/ingest/vendor/", []httpx.Audience{httpx.Public}, httpx.None(), h.withAPIKey(http.HandlerFunc(h.ingestVendor)))
	routes.HandleFunc("/tracking/alerts", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.alerts"), h.alert)
	routes.HandleFunc("/tracking/vehicles", []httpx.Audience{httpx.Tenant, httpx.Service}, httpx.On("tenant.tracking"), h.listVehicles)
	routes.HandleFunc("/tracking/vehicles/", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.tracking"), h.vehiclesResource)
	routes.HandleFunc("/tracking/devices/", []httpx.Audience{httpx.Tenant}, httpx.On("tenant.devices"), h.deviceResource)
	routes.HandleFunc("/tracking/ws", []httpx.Audience{httpx.Admin}, httpx.On("admin.devices"), h.wsBridge.ServeHTTP)
	routes.HandleFunc("/tracking/internal/gate/blocked-devices", []httpx.Audience{httpx.Service}, httpx.None(), h.blockedDevices)
	routes.HandleFunc("/tracking/internal/quarantine", []httpx.Audience{httpx.Service}, httpx.None(), h.quarantineList)
	routes.HandleFunc("/tracking/internal/quarantine/", []httpx.Audience{httpx.Service}, httpx.None(), h.quarantineAction)
	routes.HandleFunc("/tracking/internal/messages/hourly", []httpx.Audience{httpx.Service}, httpx.None(), h.messagesHourly)
	routes.HandleFunc("/health", []httpx.Audience{httpx.Public}, httpx.None(), h.health)
}

func (h *Handler) withAPIKey(next http.Handler) http.Handler {
	return httpx.WithAPIKey(next, h.gpsAPIKey, h.allowOpenIngest)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteSuccess(w, http.StatusOK, "ok", map[string]any{"service": "tracking"})
}

func (h *Handler) ingest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.ingestWithAdapter(w, r, genericAdapter{})
}

func (h *Handler) ingestVendor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	vendor := strings.TrimPrefix(r.URL.Path, "/tracking/ingest/vendor/")
	adapter, ok := resolveAdapter(vendor)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "unknown vendor: "+vendor)
		return
	}
	h.ingestWithAdapter(w, r, adapter)
}

func (h *Handler) ingestWithAdapter(w http.ResponseWriter, r *http.Request, adapter vendorAdapter) {
	cmd, err := adapter.decode(r)
	if err == nil && strings.TrimSpace(cmd.IMEI) == "" {
		err = errIMEIRequired
	}
	if errors.Is(err, errIMEIRequired) {
		h.imeiMissing(w, r)
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := h.ingestLocationUseCase.Execute(r.Context(), cmd)
	if errors.Is(err, application.ErrWarmingUp) {
		httpx.WriteError(w, http.StatusServiceUnavailable, "warming up, retry")
		return
	}
	if errors.Is(err, application.ErrIdentityUnavailable) {
		httpx.WriteError(w, http.StatusServiceUnavailable, "device lookup unavailable, retry")
		return
	}
	if errors.Is(err, application.ErrIngestUnavailable) {
		httpx.WriteError(w, http.StatusServiceUnavailable, "location storage unavailable, retry")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteOK(w, http.StatusAccepted, result)
}

func (h *Handler) alert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, ok := h.resolveTenantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authenticated tenant context is required")
		return
	}

	var req alertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.publishAlertUseCase.Execute(r.Context(), application.PublishAlertCommand{
		TenantID:  tenantID,
		AlertType: req.AlertType,
		Severity:  req.Severity,
		VehicleID: req.VehicleID,
		JobID:     req.JobID,
		Details:   req.Details,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	httpx.WriteOK(w, http.StatusAccepted, result)
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "tracking",
		Action:     "tracking.alert.published",
		EntityType: "alert",
		EntityID:   result.AlertID,
		Summary:    "Tracking alert published",
		Metadata: map[string]any{
			"alert_type": req.AlertType,
			"severity":   req.Severity,
			"vehicle_id": req.VehicleID,
			"job_id":     req.JobID,
			"subject":    result.Subject,
			"event_id":   result.EventID,
		},
	})
}

func (h *Handler) listVehicles(w http.ResponseWriter, r *http.Request) {
	if h.trackingQueryUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "tracking query is not configured")
		return
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, ok := h.resolveTenantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authenticated tenant context is required")
		return
	}
	if rejectClientTenantQuery(w, r) {
		return
	}

	locations, err := h.trackingQueryUseCase.ListLatestLocations(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteOK(w, http.StatusOK, locations)
}

func (h *Handler) resolveTenantID(r *http.Request) (string, bool) {
	tenantID := strings.TrimSpace(httpx.TenantIDFromRequest(r))
	if tenantID == "" {
		return "", false
	}
	return tenantID, true
}

func rejectClientTenantQuery(w http.ResponseWriter, r *http.Request) bool {
	query := r.URL.Query()
	if _, ok := query["tenant_id"]; ok {
		httpx.WriteError(w, http.StatusBadRequest, "tenant_id must not be supplied; tenant context is derived from the access token")
		return true
	}
	if _, ok := query["tenantId"]; ok {
		httpx.WriteError(w, http.StatusBadRequest, "tenantId must not be supplied; tenant context is derived from the access token")
		return true
	}
	return false
}

func (h *Handler) vehiclesResource(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/tracking/vehicles/")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) == 2 && parts[1] == "history" {
		h.vehicleHistory(w, r, parts[0])
		return
	}

	httpx.WriteError(w, http.StatusNotFound, "not found")
}

func (h *Handler) vehicleHistory(w http.ResponseWriter, r *http.Request, vehicleID string) {
	if h.trackingQueryUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "tracking query is not configured")
		return
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, ok := h.resolveTenantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authenticated tenant context is required")
		return
	}
	if rejectClientTenantQuery(w, r) {
		return
	}

	limit := parseLimit(r.URL.Query().Get("limit"))
	history, err := h.trackingQueryUseCase.VehicleHistory(r.Context(), tenantID, vehicleID, limit)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	httpx.WriteOK(w, http.StatusOK, map[string]any{
		"vehicle_id": vehicleID,
		"history":    history,
	})
}

func (h *Handler) deviceResource(w http.ResponseWriter, r *http.Request) {
	if h.deviceTelemetryQueryUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "telemetry query is not configured")
		return
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID, ok := h.resolveTenantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authenticated tenant context is required")
		return
	}
	if rejectClientTenantQuery(w, r) {
		return
	}

	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/tracking/devices/"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	deviceID, tab := parts[0], parts[1]
	ctx := r.Context()

	var (
		payload any
		err     error
	)
	switch tab {
	case "overview":
		payload, err = h.deviceTelemetryQueryUseCase.Overview(ctx, tenantID, deviceID)
	case "connectivity":
		payload, err = h.deviceTelemetryQueryUseCase.Connectivity(ctx, tenantID, deviceID)
	case "telemetry":
		payload, err = h.deviceTelemetryQueryUseCase.Telemetry(ctx, tenantID, deviceID, parseLimit(r.URL.Query().Get("limit")))
	case "firmware":
		payload, err = h.deviceTelemetryQueryUseCase.Firmware(ctx, tenantID, deviceID)
	default:
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteOK(w, http.StatusOK, payload)
}

func parseLimit(value string) int {
	if value == "" {
		return 0
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return limit
}

func (h *Handler) publishActivity(r *http.Request, command activitylog.PublishCommand) {
	if h.activityPublisher == nil {
		return
	}
	if claims, ok := httpx.ClaimsFromRequest(r); ok && claims != nil {
		if command.ActorID == "" {
			command.ActorID = strings.TrimSpace(claims.Subject)
		}
		if command.ActorRole == "" {
			command.ActorRole = strings.TrimSpace(claims.Role)
		}
	}
	_ = h.activityPublisher.Publish(r.Context(), command)
}

func (h *Handler) blockedDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil || claims.Service != "auth" {
		httpx.NotFound(w, r)
		return
	}
	httpx.WriteOK(w, http.StatusOK, h.gate.BlockedDevices())
}

func (h *Handler) messagesHourly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil || claims.Service != "auth" {
		httpx.NotFound(w, r)
		return
	}
	httpx.WriteOK(w, http.StatusOK, h.rate.Report(time.Now()))
}

func (h *Handler) quarantineList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil || claims.Service != "auth" {
		httpx.NotFound(w, r)
		return
	}
	devices, err := h.quarantine.List(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "quarantine list unavailable")
		return
	}
	httpx.WriteOK(w, http.StatusOK, map[string]any{"devices": devices})
}

func (h *Handler) imeiMissing(w http.ResponseWriter, r *http.Request) {
	total := h.missingIMEI.Add(1)
	if total == 1 || total%100 == 0 {
		slog.Warn("ingest_imei_missing", "total", total, "path", r.URL.Path, "user_agent", r.UserAgent())
	}
	httpx.WriteError(w, http.StatusBadRequest, "imei is required", "imei: this field is required and must not be empty")
}

func (h *Handler) quarantineAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil || claims.Service != "auth" {
		httpx.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/tracking/internal/quarantine/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		httpx.NotFound(w, r)
		return
	}
	imei, action := parts[0], parts[1]
	var err error
	switch action {
	case "dismiss":
		err = h.quarantine.Dismiss(r.Context(), imei)
	case "block":
		err = h.quarantine.Block(r.Context(), imei)
	case "unblock":
		err = h.quarantine.Unblock(r.Context(), imei)
	default:
		httpx.NotFound(w, r)
		return
	}
	if errors.Is(err, application.ErrQuarantineNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "unrecognised device not found")
		return
	}
	if errors.Is(err, application.ErrQuarantineBlocked) {
		httpx.WriteError(w, http.StatusConflict, "IMEI is blocked: unblock it first")
		return
	}
	if errors.Is(err, application.ErrQuarantineNotBlocked) {
		httpx.WriteError(w, http.StatusConflict, "IMEI is not blocked")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not update the unrecognised device")
		return
	}
	status := map[string]string{
		"dismiss": application.QuarantineDismissed,
		"block":   application.QuarantineBlocked,
		"unblock": application.QuarantineActive,
	}[action]
	httpx.WriteOK(w, http.StatusOK, map[string]any{"imei": imei, "status": status})
}
