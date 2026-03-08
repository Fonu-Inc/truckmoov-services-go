package dispatchhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	trackinggrpc "github.com/truckmoov/services-go/pkg/grpc"
)

type statusRequest struct {
	TenantID       string `json:"tenant_id"`
	JobID          string `json:"job_id"`
	PreviousStatus string `json:"previous_status,omitempty"`
	NewStatus      string `json:"new_status"`
	VehicleID      string `json:"vehicle_id,omitempty"`
	DriverID       string `json:"driver_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type Handler struct {
	publishJobStatusUseCase *application.PublishJobStatusChangedUseCase
	trackingClient          trackinggrpc.TrackingClient

	mu              sync.RWMutex
	latestLocations map[string]map[string]any
}

func NewHandler(
	publishJobStatusUseCase *application.PublishJobStatusChangedUseCase,
	trackingClient trackinggrpc.TrackingClient,
) *Handler {
	return &Handler{
		publishJobStatusUseCase: publishJobStatusUseCase,
		trackingClient:          trackingClient,
		latestLocations:         make(map[string]map[string]any),
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/dispatch/health", h.health)
	mux.HandleFunc("/dispatch/jobs/status", h.updateStatus)
	mux.HandleFunc("/dispatch/eta", h.computeETA)
	mux.HandleFunc("/health", h.health)
}

func (h *Handler) HandleLocationEvent(_ context.Context, payload map[string]any) error {
	vehicleIDValue, ok := payload["vehicle_id"]
	if !ok {
		return nil
	}
	vehicleID, ok := vehicleIDValue.(string)
	if !ok || vehicleID == "" {
		return nil
	}

	h.mu.Lock()
	h.latestLocations[vehicleID] = payload
	h.mu.Unlock()
	return nil
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "dispatch"})
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req statusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	result, err := h.publishJobStatusUseCase.Execute(r.Context(), application.PublishJobStatusChangedCommand{
		TenantID:          req.TenantID,
		JobID:             req.JobID,
		PreviousStatus:    req.PreviousStatus,
		NewStatus:         req.NewStatus,
		AssignedDriverID:  req.DriverID,
		AssignedVehicleID: req.VehicleID,
		Reason:            req.Reason,
	})
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusAccepted, result)
}

func (h *Handler) computeETA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	vehicleID := r.URL.Query().Get("vehicle_id")
	jobID := r.URL.Query().Get("job_id")
	destination := r.URL.Query().Get("destination")

	etaSeconds, err := h.trackingClient.ComputeETA(r.Context(), jobID, vehicleID, destination)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"eta_seconds": etaSeconds})
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
