package trackinghttp

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/internal/tracking/application"
)

type ingestRequest struct {
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

type Handler struct {
	ingestLocationUseCase *application.IngestLocationUseCase
	publishAlertUseCase   *application.PublishAlertUseCase
	trackingQueryUseCase  *application.TrackingQueryUseCase
	gpsAPIKey             string
}

func NewHandler(
	ingestLocationUseCase *application.IngestLocationUseCase,
	publishAlertUseCase *application.PublishAlertUseCase,
	trackingQueryUseCase *application.TrackingQueryUseCase,
	gpsAPIKey string,
) *Handler {
	return &Handler{
		ingestLocationUseCase: ingestLocationUseCase,
		publishAlertUseCase:   publishAlertUseCase,
		trackingQueryUseCase:  trackingQueryUseCase,
		gpsAPIKey:             gpsAPIKey,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/tracking/health", h.health)
	mux.HandleFunc("/tracking/ready", h.ready)
	mux.Handle("/tracking/ingest", h.withAPIKey(http.HandlerFunc(h.ingest)))
	mux.Handle("/tracking/ingest/vendor/", h.withAPIKey(http.HandlerFunc(h.ingestVendor)))
	mux.HandleFunc("/tracking/alerts", h.alert)
	mux.HandleFunc("/tracking/vehicles", h.listVehicles)
	mux.HandleFunc("/tracking/vehicles/", h.vehiclesResource)
	mux.HandleFunc("/tracking/ws", h.websocketPlaceholder)
	mux.HandleFunc("/health", h.health)
}

func (h *Handler) withAPIKey(next http.Handler) http.Handler {
	if h.gpsAPIKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Api-Key")
		if key == "" {
			key = r.URL.Query().Get("api_key")
		}
		if key != h.gpsAPIKey {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or missing api key"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tracking"})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{}
	healthy := true

	if h.ingestLocationUseCase == nil {
		checks["use_case"] = "not initialised"
		healthy = false
	} else {
		checks["use_case"] = "ok"
	}

	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}
	respondJSON(w, status, map[string]any{"ready": healthy, "checks": checks})
}

func (h *Handler) ingest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	h.ingestWithAdapter(w, r, genericAdapter{})
}

func (h *Handler) ingestVendor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	vendor := strings.TrimPrefix(r.URL.Path, "/tracking/ingest/vendor/")
	adapter, ok := resolveAdapter(vendor)
	if !ok {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown vendor: " + vendor})
		return
	}
	h.ingestWithAdapter(w, r, adapter)
}

func (h *Handler) ingestWithAdapter(w http.ResponseWriter, r *http.Request, adapter vendorAdapter) {
	cmd, err := adapter.decode(r)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := h.ingestLocationUseCase.Execute(r.Context(), cmd)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusAccepted, result)
}

func (h *Handler) alert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req alertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	result, err := h.publishAlertUseCase.Execute(r.Context(), application.PublishAlertCommand{
		TenantID:  req.TenantID,
		AlertType: req.AlertType,
		Severity:  req.Severity,
		VehicleID: req.VehicleID,
		JobID:     req.JobID,
		Details:   req.Details,
	})
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusAccepted, result)
}

func (h *Handler) websocketPlaceholder(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusNotImplemented, map[string]any{
		"status":  "placeholder",
		"message": "WebSocket live feed endpoint will be implemented in feature phase.",
	})
}

func (h *Handler) listVehicles(w http.ResponseWriter, r *http.Request) {
	if h.trackingQueryUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tracking query is not configured"})
		return
	}
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	locations, err := h.trackingQueryUseCase.ListLatestLocations(r.Context(), tenantID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"vehicles": locations})
}

func (h *Handler) vehiclesResource(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/tracking/vehicles/")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) == 2 && parts[1] == "history" {
		h.vehicleHistory(w, r, parts[0])
		return
	}

	respondJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (h *Handler) vehicleHistory(w http.ResponseWriter, r *http.Request, vehicleID string) {
	if h.trackingQueryUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tracking query is not configured"})
		return
	}
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	limit := parseLimit(r.URL.Query().Get("limit"))
	history, err := h.trackingQueryUseCase.VehicleHistory(r.Context(), tenantID, vehicleID, limit)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"vehicle_id": vehicleID,
		"history":    history,
	})
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

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
