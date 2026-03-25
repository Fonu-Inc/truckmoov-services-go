package dispatchhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
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

type stopRequest struct {
	Address      string   `json:"address"`
	ContactName  string   `json:"contact_name,omitempty"`
	ContactPhone string   `json:"contact_phone,omitempty"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
	Notes        string   `json:"notes,omitempty"`
}

type timeWindowRequest struct {
	PickupStart  *string `json:"pickup_start,omitempty"`
	PickupEnd    *string `json:"pickup_end,omitempty"`
	DropoffStart *string `json:"dropoff_start,omitempty"`
	DropoffEnd   *string `json:"dropoff_end,omitempty"`
}

type createJobRequest struct {
	TenantID                string            `json:"tenant_id,omitempty"`
	JobID                   string            `json:"job_id,omitempty"`
	Reference               string            `json:"reference,omitempty"`
	PickupAddress           string            `json:"pickupAddress,omitempty"`
	PickupLat               *float64          `json:"pickupLat,omitempty"`
	PickupLng               *float64          `json:"pickupLng,omitempty"`
	DeliveryAddress         string            `json:"deliveryAddress,omitempty"`
	DeliveryLat             *float64          `json:"deliveryLat,omitempty"`
	DeliveryLng             *float64          `json:"deliveryLng,omitempty"`
	CargoType               string            `json:"cargoType,omitempty"`
	CargoWeight             *float64          `json:"cargoWeight,omitempty"`
	CargoDescription        string            `json:"cargoDescription,omitempty"`
	Amount                  *float64          `json:"amount,omitempty"`
	Deadline                *string           `json:"deadline,omitempty"`
	Pickup                  stopRequest       `json:"pickup"`
	Dropoff                 stopRequest       `json:"dropoff"`
	RequiredVehicleType     string            `json:"required_vehicle_type,omitempty"`
	LoadWeightKg            *float64          `json:"load_weight_kg,omitempty"`
	LoadVolumeM3            float64           `json:"load_volume_m3,omitempty"`
	EstimatedServiceMinutes int               `json:"estimated_service_minutes,omitempty"`
	Priority                string            `json:"priority,omitempty"`
	TimeWindow              timeWindowRequest `json:"time_window,omitempty"`
}

type updateJobSchedulingRequest struct {
	TenantID   string            `json:"tenant_id,omitempty"`
	Priority   string            `json:"priority,omitempty"`
	TimeWindow timeWindowRequest `json:"time_window,omitempty"`
}

type assignJobRequest struct {
	TenantID  string `json:"tenant_id,omitempty"`
	DriverID  string `json:"driver_id"`
	VehicleID string `json:"vehicle_id"`
	Reason    string `json:"reason,omitempty"`
}

type cancelJobRequest struct {
	TenantID string `json:"tenant_id,omitempty"`
	Reason   string `json:"reason"`
}

type acknowledgeJobRequest struct {
	TenantID string `json:"tenant_id,omitempty"`
	DriverID string `json:"driver_id,omitempty"`
}

type updateJobStatusRequest struct {
	TenantID string `json:"tenant_id,omitempty"`
	Status   string `json:"status"`
}

type addPODAttachmentRequest struct {
	TenantID   string `json:"tenant_id,omitempty"`
	Kind       string `json:"kind"`
	URL        string `json:"url"`
	UploadedBy string `json:"uploaded_by,omitempty"`
}

type addJobNoteRequest struct {
	TenantID string `json:"tenant_id,omitempty"`
	Text     string `json:"text"`
	AuthorID string `json:"author_id,omitempty"`
}

type routeLocationRequest struct {
	Label     string  `json:"label,omitempty"`
	Address   string  `json:"address,omitempty"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type vehicleProfileRequest struct {
	VehicleID        string  `json:"vehicle_id,omitempty"`
	VehicleType      string  `json:"vehicle_type,omitempty"`
	CapacityWeightKg float64 `json:"capacity_weight_kg,omitempty"`
	CapacityVolumeM3 float64 `json:"capacity_volume_m3,omitempty"`
	AverageSpeedKPH  float64 `json:"average_speed_kph,omitempty"`
}

type driverShiftRequest struct {
	Start *string `json:"start,omitempty"`
	End   *string `json:"end,omitempty"`
}

type suggestRouteRequest struct {
	TenantID      string                `json:"tenant_id,omitempty"`
	JobID         string                `json:"job_id"`
	Origin        routeLocationRequest  `json:"origin"`
	Vehicle       vehicleProfileRequest `json:"vehicle"`
	DriverShift   driverShiftRequest    `json:"driver_shift,omitempty"`
	DepartureTime *string               `json:"departure_time,omitempty"`
}

type optimizeRouteRequest struct {
	TenantID      string                `json:"tenant_id,omitempty"`
	JobIDs        []string              `json:"job_ids"`
	Origin        routeLocationRequest  `json:"origin"`
	Vehicle       vehicleProfileRequest `json:"vehicle"`
	DriverShift   driverShiftRequest    `json:"driver_shift,omitempty"`
	DepartureTime *string               `json:"departure_time,omitempty"`
}

type Handler struct {
	publishJobStatusUseCase *application.PublishJobStatusChangedUseCase
	jobLifecycleUseCase     *application.JobLifecycleUseCase
	routingUseCase          *application.RoutingUseCase
	trackingClient          trackinggrpc.TrackingClient

	mu              sync.RWMutex
	latestLocations map[string]map[string]any
}

func NewHandler(
	publishJobStatusUseCase *application.PublishJobStatusChangedUseCase,
	jobLifecycleUseCase *application.JobLifecycleUseCase,
	routingUseCase *application.RoutingUseCase,
	trackingClient trackinggrpc.TrackingClient,
) *Handler {
	return &Handler{
		publishJobStatusUseCase: publishJobStatusUseCase,
		jobLifecycleUseCase:     jobLifecycleUseCase,
		routingUseCase:          routingUseCase,
		trackingClient:          trackingClient,
		latestLocations:         make(map[string]map[string]any),
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/dispatch/health", h.health)
	mux.HandleFunc("/dispatch/ready", h.ready)
	mux.HandleFunc("/health", h.health)
	mux.HandleFunc("/ready", h.ready)
	mux.HandleFunc("/dispatch/docs", h.docsUI)
	mux.HandleFunc("/dispatch/docs/openapi.json", h.docsOpenAPI)
	mux.HandleFunc("/docs/dispatch", h.docsUI)
	mux.HandleFunc("/docs/dispatch/openapi.json", h.docsOpenAPI)
	mux.HandleFunc("/dispatch/jobs", h.jobsCollection)
	mux.HandleFunc("/dispatch/jobs/", h.jobResource)
	mux.HandleFunc("/dispatch/routes/suggest", h.suggestRoute)
	mux.HandleFunc("/dispatch/routes/optimize", h.optimizeRoute)
	// Legacy contract kept for existing integrations that publish status events directly.
	mux.HandleFunc("/dispatch/jobs/status", h.updateStatus)
	mux.HandleFunc("/dispatch/eta", h.computeETA)
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

func (h *Handler) ready(w http.ResponseWriter, _ *http.Request) {
	ready := h.jobLifecycleUseCase != nil
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	respondJSON(w, status, map[string]any{"ready": ready, "checks": map[string]string{
		"job_lifecycle": map[bool]string{true: "ok", false: "not initialised"}[ready],
	}})
}

func (h *Handler) jobsCollection(w http.ResponseWriter, r *http.Request) {
	if h.jobLifecycleUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "job lifecycle is not configured"})
		return
	}

	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req createJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	timeWindow, err := req.TimeWindow.toDomain()
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	deadline, err := parseTimeValue(req.Deadline)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	job, err := h.jobLifecycleUseCase.CreateJob(r.Context(), application.CreateJobCommand{
		TenantID:                req.TenantID,
		JobID:                   req.JobID,
		Reference:               req.Reference,
		Pickup:                  req.pickupStop().toDomain(),
		Dropoff:                 req.dropoffStop().toDomain(),
		CargoType:               req.CargoType,
		CargoDescription:        req.CargoDescription,
		AmountNgn:               req.amountNgn(),
		Deadline:                deadline,
		RequiredVehicleType:     req.RequiredVehicleType,
		LoadWeightKg:            req.loadWeightKg(),
		LoadVolumeM3:            req.LoadVolumeM3,
		EstimatedServiceMinutes: req.EstimatedServiceMinutes,
		Priority:                req.Priority,
		TimeWindow:              timeWindow,
	})
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusCreated, job)
}

func (h *Handler) jobResource(w http.ResponseWriter, r *http.Request) {
	if h.jobLifecycleUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "job lifecycle is not configured"})
		return
	}

	jobID, action, ok := parseJobResourcePath(r.URL.Path)
	if !ok {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	switch action {
	case "":
		h.getJob(w, r, jobID)
	case "scheduling":
		h.updateJobScheduling(w, r, jobID)
	case "assign":
		h.assignJob(w, r, jobID)
	case "cancel":
		h.cancelJob(w, r, jobID)
	case "acknowledge":
		h.acknowledgeJob(w, r, jobID)
	case "status":
		h.updateJobLifecycleStatus(w, r, jobID)
	case "timeline":
		h.getJobTimeline(w, r, jobID)
	case "pod-attachments":
		h.addPODAttachment(w, r, jobID)
	case "notes":
		h.addJobNote(w, r, jobID)
	default:
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	job, err := h.jobLifecycleUseCase.GetJob(r.Context(), tenantID, jobID)
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
}

func (h *Handler) updateJobScheduling(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req updateJobSchedulingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	timeWindow, err := req.TimeWindow.toDomain()
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	job, err := h.jobLifecycleUseCase.UpdateScheduling(r.Context(), application.UpdateJobSchedulingCommand{
		TenantID:   req.TenantID,
		JobID:      jobID,
		Priority:   req.Priority,
		TimeWindow: timeWindow,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
}

func (h *Handler) assignJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req assignJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	job, err := h.jobLifecycleUseCase.AssignJob(r.Context(), application.AssignJobCommand{
		TenantID:  req.TenantID,
		JobID:     jobID,
		DriverID:  req.DriverID,
		VehicleID: req.VehicleID,
		Reason:    req.Reason,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
}

func (h *Handler) cancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req cancelJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	job, err := h.jobLifecycleUseCase.CancelJob(r.Context(), application.CancelJobCommand{
		TenantID: req.TenantID,
		JobID:    jobID,
		Reason:   req.Reason,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
}

func (h *Handler) acknowledgeJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req acknowledgeJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	job, err := h.jobLifecycleUseCase.AcknowledgeJob(r.Context(), application.AcknowledgeJobCommand{
		TenantID: req.TenantID,
		JobID:    jobID,
		DriverID: req.DriverID,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
}

func (h *Handler) updateJobLifecycleStatus(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req updateJobStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	job, err := h.jobLifecycleUseCase.UpdateJobStatus(r.Context(), application.UpdateJobStatusCommand{
		TenantID: req.TenantID,
		JobID:    jobID,
		Status:   req.Status,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
}

func (h *Handler) getJobTimeline(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	timeline, err := h.jobLifecycleUseCase.GetTimeline(r.Context(), tenantID, jobID)
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"job_id":   jobID,
		"timeline": timeline,
	})
}

func (h *Handler) addPODAttachment(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req addPODAttachmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	job, err := h.jobLifecycleUseCase.AddPODAttachment(r.Context(), application.AddJobPODAttachmentCommand{
		TenantID:   req.TenantID,
		JobID:      jobID,
		Kind:       req.Kind,
		URL:        req.URL,
		UploadedBy: req.UploadedBy,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
}

func (h *Handler) addJobNote(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req addJobNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	job, err := h.jobLifecycleUseCase.AddNote(r.Context(), application.AddJobNoteCommand{
		TenantID: req.TenantID,
		JobID:    jobID,
		Text:     req.Text,
		AuthorID: req.AuthorID,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
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

	if h.routingUseCase != nil && strings.TrimSpace(jobID) != "" {
		if origin, ok := h.lookupVehicleLocation(vehicleID); ok {
			plan, err := h.routingUseCase.EstimateETAFromLocation(
				r.Context(),
				r.URL.Query().Get("tenant_id"),
				jobID,
				origin,
				application.RouteVehicleProfile{VehicleID: vehicleID},
			)
			if err == nil {
				respondJSON(w, http.StatusOK, map[string]any{
					"eta_seconds":           plan.ETASeconds,
					"distance_km":           plan.TotalDistanceKm,
					"planned_completion_at": plan.CompletionTime,
					"source":                "dispatch-routing",
				})
				return
			}
		}
	}

	etaSeconds, err := h.trackingClient.ComputeETA(r.Context(), jobID, vehicleID, destination)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"eta_seconds": etaSeconds,
		"source":      "tracking-client",
	})
}

func (h *Handler) suggestRoute(w http.ResponseWriter, r *http.Request) {
	if h.routingUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "routing is not configured"})
		return
	}
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req suggestRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	driverShift, err := req.DriverShift.toDomain()
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	departureTime, err := parseTimeValue(req.DepartureTime)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	plan, err := h.routingUseCase.SuggestRoute(r.Context(), application.SuggestRouteCommand{
		TenantID:      req.TenantID,
		JobID:         req.JobID,
		Origin:        req.Origin.toDomain(),
		Vehicle:       req.Vehicle.toDomain(),
		DriverShift:   driverShift,
		DepartureTime: departureTime,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, plan)
}

func (h *Handler) optimizeRoute(w http.ResponseWriter, r *http.Request) {
	if h.routingUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "routing is not configured"})
		return
	}
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req optimizeRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	driverShift, err := req.DriverShift.toDomain()
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	departureTime, err := parseTimeValue(req.DepartureTime)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	plan, err := h.routingUseCase.OptimizeRoute(r.Context(), application.OptimizeRouteCommand{
		TenantID:      req.TenantID,
		JobIDs:        req.JobIDs,
		Origin:        req.Origin.toDomain(),
		Vehicle:       req.Vehicle.toDomain(),
		DriverShift:   driverShift,
		DepartureTime: departureTime,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, plan)
}

func parseJobResourcePath(path string) (jobID, action string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/dispatch/jobs/")
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", "", false
	}

	// Support exactly /dispatch/jobs/{id} and /dispatch/jobs/{id}/{action}.
	parts := strings.Split(trimmed, "/")
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func respondLifecycleError(w http.ResponseWriter, err error) {
	if err == application.ErrJobNotFound {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func (s stopRequest) toDomain() domain.JobStop {
	return domain.JobStop{
		Address:      s.Address,
		ContactName:  s.ContactName,
		ContactPhone: s.ContactPhone,
		Latitude:     s.Latitude,
		Longitude:    s.Longitude,
		Notes:        s.Notes,
	}
}

func (r createJobRequest) pickupStop() stopRequest {
	if strings.TrimSpace(r.Pickup.Address) != "" || r.Pickup.Latitude != nil || r.Pickup.Longitude != nil {
		return r.Pickup
	}

	return stopRequest{
		Address:   r.PickupAddress,
		Latitude:  r.PickupLat,
		Longitude: r.PickupLng,
	}
}

func (r createJobRequest) dropoffStop() stopRequest {
	if strings.TrimSpace(r.Dropoff.Address) != "" || r.Dropoff.Latitude != nil || r.Dropoff.Longitude != nil {
		return r.Dropoff
	}

	return stopRequest{
		Address:   r.DeliveryAddress,
		Latitude:  r.DeliveryLat,
		Longitude: r.DeliveryLng,
	}
}

func (r createJobRequest) loadWeightKg() float64 {
	if r.LoadWeightKg != nil {
		return *r.LoadWeightKg
	}
	if r.CargoWeight != nil {
		return *r.CargoWeight * 1000
	}
	return 0
}

func (r createJobRequest) amountNgn() float64 {
	if r.Amount == nil {
		return 0
	}
	return *r.Amount
}

func (t timeWindowRequest) toDomain() (domain.JobTimeWindow, error) {
	pickupStart, err := parseTimeValue(t.PickupStart)
	if err != nil {
		return domain.JobTimeWindow{}, err
	}
	pickupEnd, err := parseTimeValue(t.PickupEnd)
	if err != nil {
		return domain.JobTimeWindow{}, err
	}
	dropoffStart, err := parseTimeValue(t.DropoffStart)
	if err != nil {
		return domain.JobTimeWindow{}, err
	}
	dropoffEnd, err := parseTimeValue(t.DropoffEnd)
	if err != nil {
		return domain.JobTimeWindow{}, err
	}

	return domain.JobTimeWindow{
		PickupStart:  pickupStart,
		PickupEnd:    pickupEnd,
		DropoffStart: dropoffStart,
		DropoffEnd:   dropoffEnd,
	}, nil
}

func parseTimeValue(value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}

	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		// HTML datetime-local inputs omit timezone information; accept them and normalize to UTC.
		parsed, err = time.ParseInLocation("2006-01-02T15:04", trimmed, time.Local)
		if err != nil {
			return nil, err
		}
	}
	parsedUTC := parsed.UTC()
	return &parsedUTC, nil
}

func (d driverShiftRequest) toDomain() (application.DriverShift, error) {
	start, err := parseTimeValue(d.Start)
	if err != nil {
		return application.DriverShift{}, err
	}
	end, err := parseTimeValue(d.End)
	if err != nil {
		return application.DriverShift{}, err
	}
	return application.DriverShift{Start: start, End: end}, nil
}

func (r routeLocationRequest) toDomain() application.RouteLocation {
	return application.RouteLocation{
		Label:     r.Label,
		Address:   r.Address,
		Latitude:  r.Latitude,
		Longitude: r.Longitude,
	}
}

func (v vehicleProfileRequest) toDomain() application.RouteVehicleProfile {
	return application.RouteVehicleProfile{
		VehicleID:        v.VehicleID,
		VehicleType:      v.VehicleType,
		CapacityWeightKg: v.CapacityWeightKg,
		CapacityVolumeM3: v.CapacityVolumeM3,
		AverageSpeedKPH:  v.AverageSpeedKPH,
	}
}

func (h *Handler) lookupVehicleLocation(vehicleID string) (application.RouteLocation, bool) {
	h.mu.RLock()
	payload, ok := h.latestLocations[strings.TrimSpace(vehicleID)]
	h.mu.RUnlock()
	if !ok {
		return application.RouteLocation{}, false
	}

	latitude, okLat := readFloat(payload["latitude"])
	longitude, okLon := readFloat(payload["longitude"])
	if !okLat || !okLon {
		return application.RouteLocation{}, false
	}

	return application.RouteLocation{
		Label:     "vehicle_current_location",
		Address:   readString(payload["address"]),
		Latitude:  latitude,
		Longitude: longitude,
	}, true
}

func readFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case string:
		number, err := json.Number(strings.TrimSpace(typed)).Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func readString(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
