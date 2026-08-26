package dispatchhttp

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/internal/dispatch/infrastructure"
	"github.com/truckmoov/services-go/pkg/activitylog"
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
	Stops                   []stopRequest     `json:"stops,omitempty"`
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

type updateJobDetailsRequest struct {
	Pickup           stopRequest   `json:"pickup"`
	Dropoff          stopRequest   `json:"dropoff"`
	Stops            []stopRequest `json:"stops,omitempty"`
	CargoType        string        `json:"cargo_type,omitempty"`
	CargoDescription string        `json:"cargo_description,omitempty"`
	Amount           *float64      `json:"amount,omitempty"`
	Deadline         *string       `json:"deadline,omitempty"`
	LoadWeightKg     *float64      `json:"load_weight_kg,omitempty"`
	LoadVolumeM3     float64       `json:"load_volume_m3,omitempty"`
}

func (r updateJobDetailsRequest) stops() []domain.JobStop {
	if len(r.Stops) == 0 {
		return nil
	}
	stops := make([]domain.JobStop, 0, len(r.Stops))
	for _, s := range r.Stops {
		stops = append(stops, s.toDomain())
	}
	return stops
}

func (r updateJobDetailsRequest) amountNgn() float64 {
	if r.Amount == nil {
		return 0
	}
	return *r.Amount
}

func (r updateJobDetailsRequest) loadWeightKg() float64 {
	if r.LoadWeightKg == nil {
		return 0
	}
	return *r.LoadWeightKg
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
	activityPublisher         *activitylog.Publisher
	fleetClient               *infrastructure.FleetClient
	createJobOfferUseCase     *application.CreateJobOfferUseCase
	acceptJobOfferUseCase     *application.AcceptJobOfferUseCase
	declineJobOfferUseCase    *application.DeclineJobOfferUseCase
	listJobOffersUseCase      *application.ListJobOffersUseCase
	activeJobCapacityUseCase  *application.ActiveJobCapacityUseCase
}

// WithJobOffers attaches the job offer use cases.
//
// A builder rather than more constructor arguments, matching the notification
// service's WithCampaigns: the offer routes report unconfigured when this is
// never called, so a deployment can start without it.
func (h *Handler) WithJobOffers(
	create *application.CreateJobOfferUseCase,
	accept *application.AcceptJobOfferUseCase,
	decline *application.DeclineJobOfferUseCase,
	list *application.ListJobOffersUseCase,
	capacity *application.ActiveJobCapacityUseCase,
) *Handler {
	h.createJobOfferUseCase = create
	h.acceptJobOfferUseCase = accept
	h.declineJobOfferUseCase = decline
	h.listJobOffersUseCase = list
	h.activeJobCapacityUseCase = capacity
	return h
}

func NewHandler(
	publishJobStatusUseCase *application.PublishJobStatusChangedUseCase,
	jobLifecycleUseCase *application.JobLifecycleUseCase,
	routingUseCase *application.RoutingUseCase,
	sendJobInstructionUseCase *application.SendJobInstructionUseCase,
	activityPublisher *activitylog.Publisher,
	fleetClient *infrastructure.FleetClient,
) *Handler {
	return &Handler{
		publishJobStatusUseCase:   publishJobStatusUseCase,
		jobLifecycleUseCase:       jobLifecycleUseCase,
		routingUseCase:            routingUseCase,
		sendJobInstructionUseCase: sendJobInstructionUseCase,
		activityPublisher:         activityPublisher,
		fleetClient:               fleetClient,
	}
}

// jobWithAssignments flattens domain.Job and adds driver/vehicle display details
// fetched from the fleet service, so API consumers don't need a second round-trip.
type jobWithAssignments struct {
	domain.Job
	AssignedDriverName   string `json:"assigned_driver_name,omitempty"`
	AssignedVehicleReg   string `json:"assigned_vehicle_reg,omitempty"`
	AssignedVehicleMake  string `json:"assigned_vehicle_make,omitempty"`
	AssignedVehicleModel string `json:"assigned_vehicle_model,omitempty"`
}

func (h *Handler) enrichJob(ctx context.Context, tenantID string, job domain.Job) jobWithAssignments {
	enriched := jobWithAssignments{Job: job}
	if h.fleetClient == nil {
		return enriched
	}
	if job.AssignedDriverID != "" {
		if driver, err := h.fleetClient.GetDriver(ctx, tenantID, job.AssignedDriverID); err == nil {
			enriched.AssignedDriverName = driver.Name
		} else {
			log.Printf("dispatch: enrichJob: fleet GetDriver failed for driver_id=%s tenant_id=%s: %v", job.AssignedDriverID, tenantID, err)
		}
	}
	if job.AssignedVehicleID != "" {
		if vehicle, err := h.fleetClient.GetVehicle(ctx, tenantID, job.AssignedVehicleID); err == nil {
			enriched.AssignedVehicleReg = vehicle.PlateNumber
			enriched.AssignedVehicleMake = vehicle.Make
			enriched.AssignedVehicleModel = vehicle.Model
		} else {
			log.Printf("dispatch: enrichJob: fleet GetVehicle failed for vehicle_id=%s tenant_id=%s: %v", job.AssignedVehicleID, tenantID, err)
		}
	}
	return enriched
}

func (h *Handler) enrichJobs(ctx context.Context, tenantID string, jobs []domain.Job) []jobWithAssignments {
	enriched := make([]jobWithAssignments, len(jobs))
	for i, job := range jobs {
		enriched[i] = h.enrichJob(ctx, tenantID, job)
	}
	return enriched
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/dispatch/health", h.health)
	mux.HandleFunc("/health", h.health)
	mux.HandleFunc("/dispatch/docs", h.docsUI)
	mux.HandleFunc("/dispatch/docs/openapi.json", h.docsOpenAPI)
	mux.HandleFunc("/docs/dispatch", h.docsUI)
	mux.HandleFunc("/docs/dispatch/openapi.json", h.docsOpenAPI)
	mux.HandleFunc("/dispatch/jobs", h.jobsCollection)
	mux.HandleFunc("/dispatch/jobs/summary", h.jobsSummary)
	mux.HandleFunc("/dispatch/jobs/", h.jobResource)
	mux.HandleFunc("/dispatch/routes/optimize", h.optimizeRoute)
	// Legacy contract kept for existing integrations that publish status events directly.
	mux.HandleFunc("/dispatch/jobs/status", h.updateStatus)
	mux.HandleFunc("/dispatch/job-offers", h.jobOffers)
	mux.HandleFunc("/dispatch/job-offers/", h.jobOfferByPath)
	mux.HandleFunc("/dispatch/job-offers/capacity", h.jobOfferCapacity)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "dispatch"})
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
		Stops:                   req.stops(),
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

	respondJSON(w, http.StatusCreated, map[string]any{"status": true, "message": "success", "data": job})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.created",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Dispatch job created",
		Metadata: map[string]any{
			"reference":           job.Reference,
			"assigned_driver_id":  job.AssignedDriverID,
			"assigned_vehicle_id": job.AssignedVehicleID,
			"status":              job.Status,
		},
	})
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

func (h *Handler) jobsSummary(w http.ResponseWriter, r *http.Request) {
	if h.jobLifecycleUseCase == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "job lifecycle is not configured"})
		return
	}
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	if rejectClientTenantQuery(w, r) {
		return
	}

	summary, err := h.jobLifecycleUseCase.SummarizeJobs(r.Context(), tenantID)
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": summary})
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	if rejectClientTenantQuery(w, r) {
		return
	}

	q := r.URL.Query()
	filter := application.ListJobsFilter{
		Status:     q.Get("status"),
		Query:      q.Get("query"),
		DriverID:   q.Get("driver_id"),
		VehicleID:  q.Get("vehicle_id"),
		AmountFrom: parseOptionalFloat(q.Get("amount_from")),
		AmountTo:   parseOptionalFloat(q.Get("amount_to")),
		Deadline:   parseOptionalDate(q.Get("deadline")),
	}
	// UI submits weight in tons; the domain stores kilograms.
	if from := parseOptionalFloat(q.Get("weight_from")); from != nil {
		kg := *from * 1000
		filter.WeightFromKg = &kg
	}
	if to := parseOptionalFloat(q.Get("weight_to")); to != nil {
		kg := *to * 1000
		filter.WeightToKg = &kg
	}

	jobs, err := h.jobLifecycleUseCase.ListJobs(r.Context(), tenantID, filter)
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": h.enrichJobs(r.Context(), tenantID, jobs)})
}

// parseOptionalFloat returns a pointer to the parsed value, or nil when the
// query param is absent or unparseable (treated as "no filter").
func parseOptionalFloat(raw string) *float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &v
}

// parseOptionalDate parses a calendar date (YYYY-MM-DD, or a full RFC3339
// timestamp) into midnight UTC of that day, or nil when absent/unparseable.
func parseOptionalDate(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return &t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return &day
	}
	return nil
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
		switch r.Method {
		case http.MethodGet:
			h.getJob(w, r, jobID)
		case http.MethodPatch, http.MethodPut:
			h.updateJobDetails(w, r, jobID)
		case http.MethodDelete:
			h.deleteJob(w, r, jobID)
		default:
			respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
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
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": h.enrichJob(r.Context(), tenantID, job)})
}

func (h *Handler) updateJobDetails(w http.ResponseWriter, r *http.Request, jobID string) {
	var req updateJobDetailsRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
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

	job, err := h.jobLifecycleUseCase.UpdateDetails(r.Context(), application.UpdateJobDetailsCommand{
		TenantID:         tenantID,
		JobID:            jobID,
		Pickup:           req.Pickup.toDomain(),
		Dropoff:          req.Dropoff.toDomain(),
		Stops:            req.stops(),
		CargoType:        req.CargoType,
		CargoDescription: req.CargoDescription,
		AmountNgn:        req.amountNgn(),
		Deadline:         deadline,
		LoadWeightKg:     req.loadWeightKg(),
		LoadVolumeM3:     req.LoadVolumeM3,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.details.updated",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Dispatch job details updated",
		Metadata: map[string]any{
			"amount_ngn": job.AmountNgn,
			"deadline":   job.Deadline,
		},
	})
}

func (h *Handler) deleteJob(w http.ResponseWriter, r *http.Request, jobID string) {
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	if err := h.jobLifecycleUseCase.DeleteJob(r.Context(), application.DeleteJobCommand{
		TenantID: tenantID,
		JobID:    jobID,
	}); err != nil {
		respondLifecycleError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success"})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.deleted",
		EntityType: "job",
		EntityID:   jobID,
		Summary:    "Dispatch job deleted",
	})
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
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.scheduling.updated",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Dispatch job scheduling updated",
		Metadata: map[string]any{
			"priority":    job.Priority,
			"time_window": job.TimeWindow,
		},
	})
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
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.assigned",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Dispatch job assigned",
		Metadata: map[string]any{
			"driver_id":  req.DriverID,
			"vehicle_id": req.VehicleID,
			"reason":     req.Reason,
			"status":     job.Status,
		},
	})
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
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.cancelled",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Dispatch job cancelled",
		Metadata: map[string]any{
			"reason": req.Reason,
			"status": job.Status,
		},
	})
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
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.acknowledged",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Dispatch job acknowledged",
		Metadata: map[string]any{
			"driver_id": req.DriverID,
			"status":    job.Status,
		},
	})
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
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
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
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.driver_status_request.created",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Driver status update request captured",
		Metadata: map[string]any{
			"request_id":    request.ID,
			"requested_for": request.RequestedStatus,
			"driver_id":     request.DriverID,
			"driver_phone":  request.DriverPhone,
			"channel":       request.Channel,
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
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.driver_status_request.confirmed",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Driver status update request confirmed",
		Metadata: map[string]any{
			"request_id":    request.ID,
			"confirmed_by":  confirmedBy,
			"requested_for": request.RequestedStatus,
			"status":        job.Status,
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
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.whatsapp_instruction.sent",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "WhatsApp job instruction sent",
		Metadata: map[string]any{
			"driver_id":     firstNonEmptyDispatchValue(req.DriverID, req.DriverIDAlt),
			"template_name": firstNonEmptyDispatchValue(req.TemplateName, req.TemplateNameAlt),
			"status":        job.Status,
		},
	})
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
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.pod_attachment.added",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Job POD attachment added",
		Metadata: map[string]any{
			"kind":        req.Kind,
			"url":         req.URL,
			"uploaded_by": req.UploadedBy,
		},
	})
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
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": job})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "dispatch",
		Action:     "job.note.added",
		EntityType: "job",
		EntityID:   job.ID,
		Summary:    "Dispatch job note added",
		Metadata: map[string]any{
			"author_id": req.AuthorID,
		},
	})
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

func (h *Handler) publishActivity(r *http.Request, command activitylog.PublishCommand) {
	if h.activityPublisher == nil {
		return
	}

	if claims, ok := httpx.ClaimsFromRequest(r); ok && claims != nil {
		if command.ActorID == "" {
			command.ActorID = firstNonEmptyDispatchValue(strings.TrimSpace(claims.Subject), strings.TrimSpace(claims.Email))
		}
		if command.ActorRole == "" {
			command.ActorRole = strings.TrimSpace(claims.Role)
		}
	}

	if command.OccurredAt.IsZero() {
		command.OccurredAt = time.Now().UTC()
	}

	if err := h.activityPublisher.Publish(r.Context(), command); err != nil {
		// Activity logging is best-effort and should not fail the primary request path.
	}
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

func (r createJobRequest) stops() []domain.JobStop {
	if len(r.Stops) == 0 {
		return nil
	}
	stops := make([]domain.JobStop, 0, len(r.Stops))
	for _, s := range r.Stops {
		stops = append(stops, s.toDomain())
	}
	return stops
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
