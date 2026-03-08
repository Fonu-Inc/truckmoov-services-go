package trackinghttp

import (
	"encoding/json"
	"net/http"

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
}

func NewHandler(
	ingestLocationUseCase *application.IngestLocationUseCase,
	publishAlertUseCase *application.PublishAlertUseCase,
) *Handler {
	return &Handler{
		ingestLocationUseCase: ingestLocationUseCase,
		publishAlertUseCase:   publishAlertUseCase,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/tracking/health", h.health)
	mux.HandleFunc("/tracking/ingest", h.ingest)
	mux.HandleFunc("/tracking/alerts", h.alert)
	mux.HandleFunc("/tracking/ws", h.websocketPlaceholder)
	mux.HandleFunc("/health", h.health)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tracking"})
}

func (h *Handler) ingest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	result, err := h.ingestLocationUseCase.Execute(r.Context(), application.IngestLocationCommand{
		TenantID:  req.TenantID,
		VehicleID: req.VehicleID,
		DeviceID:  req.DeviceID,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		SpeedKPH:  req.SpeedKPH,
		Heading:   req.Heading,
		AccuracyM: req.AccuracyM,
		FixTime:   req.FixTime,
	})
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

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
