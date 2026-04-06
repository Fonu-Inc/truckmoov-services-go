package dispatchhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type statusRequest struct {
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
	JobID                   string            `json:"job_id,omitempty"`
	Reference               string            `json:"reference,omitempty"`
	VehicleID               string            `json:"vehicleId,omitempty"`
	VehicleIDSnake          string            `json:"vehicle_id,omitempty"`
	DriverID                string            `json:"driverId,omitempty"`
	DriverIDSnake           string            `json:"driver_id,omitempty"`
	DriverPhone             string            `json:"driverPhone,omitempty"`
	DriverPhoneSnake        string            `json:"driver_phone,omitempty"`
	PickupLocation          string            `json:"pickupLocation,omitempty"`
	PickupAddress           string            `json:"pickupAddress,omitempty"`
	PickupLat               *float64          `json:"pickupLat,omitempty"`
	PickupLng               *float64          `json:"pickupLng,omitempty"`
	DeliveryLocation        string            `json:"deliveryLocation,omitempty"`
	DeliveryAddress         string            `json:"deliveryAddress,omitempty"`
	DeliveryLat             *float64          `json:"deliveryLat,omitempty"`
	DeliveryLng             *float64          `json:"deliveryLng,omitempty"`
	CargoType               string            `json:"cargoType,omitempty"`
	CargoWeight             *float64          `json:"cargoWeight,omitempty"`
	CargoDescription        string            `json:"cargoDescription,omitempty"`
	AdditionalNotes         string            `json:"additionalNotes,omitempty"`
	Notes                   string            `json:"notes,omitempty"`
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
	Priority   string            `json:"priority,omitempty"`
	TimeWindow timeWindowRequest `json:"time_window,omitempty"`
}

type assignJobRequest struct {
	DriverID       string `json:"driver_id"`
	DriverPhone    string `json:"driver_phone,omitempty"`
	DriverPhoneAlt string `json:"driverPhone,omitempty"`
	VehicleID      string `json:"vehicle_id"`
	Reason         string `json:"reason,omitempty"`
}

type cancelJobRequest struct {
	Reason string `json:"reason"`
}

type acknowledgeJobRequest struct {
	DriverID string `json:"driver_id,omitempty"`
}

type updateJobStatusRequest struct {
	Status string `json:"status"`
}

type addPODAttachmentRequest struct {
	Kind       string `json:"kind"`
	URL        string `json:"url"`
	UploadedBy string `json:"uploaded_by,omitempty"`
}

type addJobNoteRequest struct {
	Text     string `json:"text"`
	AuthorID string `json:"author_id,omitempty"`
}

type sendJobInstructionHeaderMediaRequest struct {
	Type     string `json:"type,omitempty"`
	Link     string `json:"link,omitempty"`
	ID       string `json:"id,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type sendJobInstructionButtonParameterRequest struct {
	Index   int    `json:"index"`
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Payload string `json:"payload,omitempty"`
}

type sendJobInstructionRequest struct {
	DriverID            string                                     `json:"driver_id,omitempty"`
	DriverIDAlt         string                                     `json:"driverId,omitempty"`
	To                  string                                     `json:"to,omitempty"`
	Recipient           string                                     `json:"recipient,omitempty"`
	TemplateName        string                                     `json:"template_name,omitempty"`
	TemplateNameAlt     string                                     `json:"templateName,omitempty"`
	Language            string                                     `json:"language,omitempty"`
	DriverName          string                                     `json:"driver_name,omitempty"`
	DriverNameAlt       string                                     `json:"driverName,omitempty"`
	HeaderMedia         *sendJobInstructionHeaderMediaRequest      `json:"header_media,omitempty"`
	HeaderMediaAlt      *sendJobInstructionHeaderMediaRequest      `json:"headerMedia,omitempty"`
	ButtonParameters    []sendJobInstructionButtonParameterRequest `json:"button_parameters,omitempty"`
	ButtonParametersAlt []sendJobInstructionButtonParameterRequest `json:"buttonParameters,omitempty"`
}

type createDriverStatusRequestRequest struct {
	DriverID           string `json:"driver_id,omitempty"`
	DriverIDAlt        string `json:"driverId,omitempty"`
	DriverPhone        string `json:"driver_phone,omitempty"`
	DriverPhoneAlt     string `json:"driverPhone,omitempty"`
	Keyword            string `json:"keyword,omitempty"`
	RawMessage         string `json:"raw_message,omitempty"`
	RawMessageAlt      string `json:"rawMessage,omitempty"`
	SourceMessageID    string `json:"source_message_id,omitempty"`
	SourceMessageIDAlt string `json:"sourceMessageID,omitempty"`
	Channel            string `json:"channel,omitempty"`
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

type optimizeRouteRequest struct {
	JobIDs        []string              `json:"job_ids"`
	Origin        routeLocationRequest  `json:"origin"`
	Vehicle       vehicleProfileRequest `json:"vehicle"`
	DriverShift   driverShiftRequest    `json:"driver_shift,omitempty"`
	DepartureTime *string               `json:"departure_time,omitempty"`
}

type Handler struct {
	publishJobStatusUseCase   *application.PublishJobStatusChangedUseCase
	jobLifecycleUseCase       *application.JobLifecycleUseCase
	routingUseCase            *application.RoutingUseCase
	sendJobInstructionUseCase *application.SendJobInstructionUseCase
}

func NewHandler(
	publishJobStatusUseCase *application.PublishJobStatusChangedUseCase,
	jobLifecycleUseCase *application.JobLifecycleUseCase,
	routingUseCase *application.RoutingUseCase,
	sendJobInstructionUseCase *application.SendJobInstructionUseCase,
) *Handler {
	return &Handler{
		publishJobStatusUseCase:   publishJobStatusUseCase,
		jobLifecycleUseCase:       jobLifecycleUseCase,
		routingUseCase:            routingUseCase,
		sendJobInstructionUseCase: sendJobInstructionUseCase,
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
	mux.HandleFunc("/dispatch/routes/optimize", h.optimizeRoute)
	// Legacy contract kept for existing integrations that publish status events directly.
	mux.HandleFunc("/dispatch/jobs/status", h.updateStatus)
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

	switch r.Method {
	case http.MethodGet:
		h.listJobs(w, r)
		return
	case http.MethodPost:
		h.createJob(w, r)
		return
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
}

func (h *Handler) createJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	// The original Dispatch Center modal has a smaller required field set than the
	// richer legacy payload, so validate it only when those modal-specific fields are present.
	if err := req.validateOriginalModalPayload(); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	job, err := h.jobLifecycleUseCase.CreateJob(r.Context(), application.CreateJobCommand{
		TenantID:                tenantID,
		JobID:                   req.JobID,
		Reference:               req.Reference,
		VehicleID:               req.vehicleID(),
		DriverID:                req.driverID(),
		DriverPhone:             req.driverPhone(),
		Pickup:                  req.pickupStop().toDomain(),
		Dropoff:                 req.dropoffStop().toDomain(),
		CargoType:               req.CargoType,
		CargoDescription:        req.CargoDescription,
		InitialNote:             req.initialNote(),
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

func (h *Handler) resolveTenantID(r *http.Request) (string, error) {
	tenantID := strings.TrimSpace(httpx.TenantIDFromRequest(r))
	if tenantID == "" {
		return "", errors.New("authenticated tenant context is required")
	}
	return tenantID, nil
}

func (h *Handler) requireTenantID(w http.ResponseWriter, r *http.Request) (string, bool) {
	tenantID, err := h.resolveTenantID(r)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return "", false
	}
	return tenantID, true
}

func decodeJSONBody(r *http.Request, dest any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dest)
}

func rejectClientTenantQuery(w http.ResponseWriter, r *http.Request) bool {
	query := r.URL.Query()
	if _, ok := query["tenant_id"]; ok {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id must not be supplied; tenant context is derived from the access token"})
		return true
	}
	if _, ok := query["tenantId"]; ok {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "tenantId must not be supplied; tenant context is derived from the access token"})
		return true
	}
	return false
}

func authenticatedDispatcherTenantID(r *http.Request) (string, int, bool) {
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil {
		return "", http.StatusUnauthorized, false
	}
	if strings.TrimSpace(claims.TenantID) == "" {
		return "", http.StatusUnauthorized, false
	}
	if !isDispatcherCapableRole(claims.Role) {
		return "", http.StatusForbidden, false
	}
	return strings.TrimSpace(claims.TenantID), http.StatusOK, true
}

func isDispatcherCapableRole(role string) bool {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case "DISPATCHER", "OPERATIONS_MANAGER", "FLEET_ADMIN", "PLATFORM_ADMIN", "ENTERPRISE_ADMIN":
		return true
	default:
		return false
	}
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	if rejectClientTenantQuery(w, r) {
		return
	}

	jobs, err := h.jobLifecycleUseCase.ListJobs(r.Context(), tenantID, application.ListJobsFilter{
		Status:    r.URL.Query().Get("status"),
		Query:     r.URL.Query().Get("query"),
		DriverID:  r.URL.Query().Get("driver_id"),
		VehicleID: r.URL.Query().Get("vehicle_id"),
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": jobs})
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
	case "send-whatsapp-instruction":
		h.sendWhatsAppInstruction(w, r, jobID)
	case "driver-status-requests":
		h.createDriverStatusRequest(w, r, jobID)
	case "pod-attachments":
		h.addPODAttachment(w, r, jobID)
	case "notes":
		h.addJobNote(w, r, jobID)
	default:
		if strings.HasPrefix(action, "driver-status-requests/") && strings.HasSuffix(action, "/confirm") {
			requestID := strings.TrimSuffix(strings.TrimPrefix(action, "driver-status-requests/"), "/confirm")
			requestID = strings.Trim(requestID, "/")
			if requestID == "" {
				respondJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			h.confirmDriverStatusRequest(w, r, jobID, requestID)
			return
		}
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	job, err := h.jobLifecycleUseCase.GetJob(r.Context(), tenantID, jobID)
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
}

func (h *Handler) updateJobScheduling(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req updateJobSchedulingRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	timeWindow, err := req.TimeWindow.toDomain()
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	job, err := h.jobLifecycleUseCase.UpdateScheduling(r.Context(), application.UpdateJobSchedulingCommand{
		TenantID:   tenantID,
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
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	job, err := h.jobLifecycleUseCase.AssignJob(r.Context(), application.AssignJobCommand{
		TenantID:    tenantID,
		JobID:       jobID,
		DriverID:    req.DriverID,
		DriverPhone: firstNonEmptyDispatchValue(req.DriverPhone, req.DriverPhoneAlt),
		VehicleID:   req.VehicleID,
		Reason:      req.Reason,
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
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	job, err := h.jobLifecycleUseCase.CancelJob(r.Context(), application.CancelJobCommand{
		TenantID: tenantID,
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
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	job, err := h.jobLifecycleUseCase.AcknowledgeJob(r.Context(), application.AcknowledgeJobCommand{
		TenantID: tenantID,
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

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		respondJSON(w, authStatus, map[string]string{"error": http.StatusText(authStatus)})
		return
	}

	var req updateJobStatusRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	job, err := h.jobLifecycleUseCase.UpdateJobStatus(r.Context(), application.UpdateJobStatusCommand{
		TenantID: tenantID,
		JobID:    jobID,
		Status:   req.Status,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, job)
}

func (h *Handler) createDriverStatusRequest(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		respondJSON(w, authStatus, map[string]string{"error": http.StatusText(authStatus)})
		return
	}

	var req createDriverStatusRequestRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	job, request, err := h.jobLifecycleUseCase.CreateDriverStatusRequest(r.Context(), application.CreateDriverStatusRequestCommand{
		TenantID:        tenantID,
		JobID:           jobID,
		DriverID:        firstNonEmptyDispatchValue(req.DriverID, req.DriverIDAlt),
		DriverPhone:     firstNonEmptyDispatchValue(req.DriverPhone, req.DriverPhoneAlt),
		Keyword:         strings.TrimSpace(req.Keyword),
		RawMessage:      firstNonEmptyDispatchValue(req.RawMessage, req.RawMessageAlt),
		SourceMessageID: firstNonEmptyDispatchValue(req.SourceMessageID, req.SourceMessageIDAlt),
		Channel:         firstNonEmptyDispatchValue(req.Channel, "whatsapp"),
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"status":  true,
		"message": "success",
		"data": map[string]any{
			"job":     job,
			"request": request,
		},
	})
}

func (h *Handler) confirmDriverStatusRequest(w http.ResponseWriter, r *http.Request, jobID, requestID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": http.StatusText(http.StatusUnauthorized)})
		return
	}
	if !isDispatcherCapableRole(claims.Role) {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": http.StatusText(http.StatusForbidden)})
		return
	}
	tenantID := strings.TrimSpace(claims.TenantID)
	if tenantID == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "authenticated tenant context is required"})
		return
	}
	confirmedBy := firstNonEmptyDispatchValue(claims.Subject, claims.Email)

	job, request, err := h.jobLifecycleUseCase.ConfirmDriverStatusRequest(r.Context(), application.ConfirmDriverStatusRequestCommand{
		TenantID:    tenantID,
		JobID:       jobID,
		RequestID:   requestID,
		ConfirmedBy: confirmedBy,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"status":  true,
		"message": "success",
		"data": map[string]any{
			"job":     job,
			"request": request,
		},
	})
}

func (h *Handler) getJobTimeline(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	timeline, err := h.jobLifecycleUseCase.GetTimeline(r.Context(), tenantID, jobID)
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": map[string]any{
		"job_id":   jobID,
		"timeline": timeline,
	}})
}

func (h *Handler) sendWhatsAppInstruction(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if h.sendJobInstructionUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "job instruction sending is not configured"})
		return
	}

	var req sendJobInstructionRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	// Dispatch owns the job payload and now persists the assigned driver's WhatsApp number on the job.
	// The optional request recipient is only accepted as a compatibility check and must match the stored value.
	job, err := h.sendJobInstructionUseCase.Execute(r.Context(), application.SendJobInstructionCommand{
		TenantID:         tenantID,
		JobID:            jobID,
		DriverID:         firstNonEmptyDispatchValue(req.DriverID, req.DriverIDAlt),
		Recipient:        firstNonEmptyDispatchValue(req.To, req.Recipient),
		TemplateName:     firstNonEmptyDispatchValue(req.TemplateName, req.TemplateNameAlt),
		Language:         strings.TrimSpace(req.Language),
		DriverName:       firstNonEmptyDispatchValue(req.DriverName, req.DriverNameAlt),
		HeaderMedia:      normalizeDispatchHeaderMedia(firstNonNilDispatchHeaderMedia(req.HeaderMedia, req.HeaderMediaAlt)),
		ButtonParameters: normalizeDispatchButtonParameters(req.ButtonParameters, req.ButtonParametersAlt),
	})
	if err != nil {
		switch {
		case errors.Is(err, application.ErrJobNotFound):
			respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case errors.Is(err, application.ErrJobInstructionJobIDRequired),
			errors.Is(err, application.ErrJobInstructionDriverIDRequired),
			errors.Is(err, application.ErrJobInstructionTemplateNameRequired),
			errors.Is(err, application.ErrJobInstructionDriverNameRequired),
			errors.Is(err, application.ErrJobInstructionCargoRequired):
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, application.ErrJobInstructionJobNotAssigned),
			errors.Is(err, application.ErrJobInstructionAssignedPhoneRequired),
			errors.Is(err, application.ErrJobInstructionDriverMismatch),
			errors.Is(err, application.ErrJobInstructionRecipientMismatch),
			errors.Is(err, application.ErrJobInstructionStatusNotSendable):
			respondJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			respondJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
}

func (h *Handler) addPODAttachment(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req addPODAttachmentRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	job, err := h.jobLifecycleUseCase.AddPODAttachment(r.Context(), application.AddJobPODAttachmentCommand{
		TenantID:   tenantID,
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
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	job, err := h.jobLifecycleUseCase.AddNote(r.Context(), application.AddJobNoteCommand{
		TenantID: tenantID,
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
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	result, err := h.publishJobStatusUseCase.Execute(r.Context(), application.PublishJobStatusChangedCommand{
		TenantID:          tenantID,
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

func (h *Handler) optimizeRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req optimizeRouteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if h.routingUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "routing is not configured"})
		return
	}

	tenantID, _ := h.resolveTenantID(r)
	departureTime, err := parseTimeValue(req.DepartureTime)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	driverShift, err := req.DriverShift.toDomain()
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	plan, err := h.routingUseCase.OptimizeRoute(r.Context(), application.OptimizeRouteCommand{
		TenantID:      tenantID,
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

	parts := strings.Split(trimmed, "/")
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

func respondLifecycleError(w http.ResponseWriter, err error) {
	if err == application.ErrJobNotFound {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err == application.ErrDriverStatusRequestNotFound {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "already has a pending driver status request") ||
		strings.Contains(strings.ToLower(err.Error()), "must be the next workflow status") ||
		strings.Contains(strings.ToLower(err.Error()), "belongs to a different driver") ||
		strings.Contains(strings.ToLower(err.Error()), "phone number does not match") ||
		strings.Contains(strings.ToLower(err.Error()), "cannot report driver status") ||
		strings.Contains(strings.ToLower(err.Error()), "cannot confirm driver status request") {
		respondJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
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

	address := strings.TrimSpace(r.PickupLocation)
	if address == "" {
		address = r.PickupAddress
	}
	return stopRequest{
		Address:   address,
		Latitude:  r.PickupLat,
		Longitude: r.PickupLng,
	}
}

func (r createJobRequest) dropoffStop() stopRequest {
	if strings.TrimSpace(r.Dropoff.Address) != "" || r.Dropoff.Latitude != nil || r.Dropoff.Longitude != nil {
		return r.Dropoff
	}

	address := strings.TrimSpace(r.DeliveryLocation)
	if address == "" {
		address = r.DeliveryAddress
	}
	return stopRequest{
		Address:   address,
		Latitude:  r.DeliveryLat,
		Longitude: r.DeliveryLng,
	}
}

func (r createJobRequest) vehicleID() string {
	if value := strings.TrimSpace(r.VehicleID); value != "" {
		return value
	}
	return strings.TrimSpace(r.VehicleIDSnake)
}

func (r createJobRequest) driverID() string {
	if value := strings.TrimSpace(r.DriverID); value != "" {
		return value
	}
	return strings.TrimSpace(r.DriverIDSnake)
}

func (r createJobRequest) driverPhone() string {
	if value := strings.TrimSpace(r.DriverPhone); value != "" {
		return value
	}
	return strings.TrimSpace(r.DriverPhoneSnake)
}

func (r createJobRequest) initialNote() string {
	if value := strings.TrimSpace(r.Notes); value != "" {
		return value
	}
	return strings.TrimSpace(r.AdditionalNotes)
}

func (r createJobRequest) validateOriginalModalPayload() error {
	if !r.usesOriginalModalShape() {
		return nil
	}
	if r.vehicleID() == "" {
		return errors.New("vehicleId is required for the original dispatch modal")
	}
	if strings.TrimSpace(r.pickupStop().Address) == "" {
		return errors.New("pickupLocation is required for the original dispatch modal")
	}
	if strings.TrimSpace(r.dropoffStop().Address) == "" {
		return errors.New("deliveryLocation is required for the original dispatch modal")
	}
	return nil
}

func (r createJobRequest) usesOriginalModalShape() bool {
	return r.vehicleID() != "" ||
		r.driverID() != "" ||
		strings.TrimSpace(r.PickupLocation) != "" ||
		strings.TrimSpace(r.DeliveryLocation) != "" ||
		r.initialNote() != ""
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

func firstNonEmptyDispatchValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonNilDispatchHeaderMedia(values ...*sendJobInstructionHeaderMediaRequest) *sendJobInstructionHeaderMediaRequest {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func normalizeDispatchHeaderMedia(value *sendJobInstructionHeaderMediaRequest) *application.JobInstructionHeaderMedia {
	if value == nil {
		return nil
	}
	return &application.JobInstructionHeaderMedia{
		Type:     strings.TrimSpace(value.Type),
		Link:     strings.TrimSpace(value.Link),
		ID:       strings.TrimSpace(value.ID),
		Filename: strings.TrimSpace(value.Filename),
	}
}

func normalizeDispatchButtonParameters(primary, alternate []sendJobInstructionButtonParameterRequest) []application.JobInstructionButtonParameter {
	selected := primary
	if len(selected) == 0 {
		selected = alternate
	}
	if len(selected) == 0 {
		return nil
	}

	normalized := make([]application.JobInstructionButtonParameter, 0, len(selected))
	for _, parameter := range selected {
		normalized = append(normalized, application.JobInstructionButtonParameter{
			Index:   parameter.Index,
			Type:    strings.TrimSpace(parameter.Type),
			Text:    strings.TrimSpace(parameter.Text),
			Payload: strings.TrimSpace(parameter.Payload),
		})
	}
	return normalized
}
func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
