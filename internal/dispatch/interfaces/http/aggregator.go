package dispatchhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

// aggregatorRoutes handles /dispatch/aggregator/* — see Handler.RegisterRoutes.
func (h *Handler) aggregatorConfigured() bool {
	return h.summaryFiguresUseCase != nil
}

func (h *Handler) aggregatorUnconfigured(w http.ResponseWriter) {
	respondError(w, http.StatusServiceUnavailable, "aggregator network is not configured")
}

func (h *Handler) aggregatorSummary(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	summary, err := h.summaryFiguresUseCase.Execute(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": summary})
}

type addOperatorRequest struct {
	OperatorTenantID string   `json:"operator_tenant_id,omitempty"`
	InvitedEmail     string   `json:"invited_email,omitempty"`
	Types            []string `json:"types"`
	FeeRatePct       float64  `json:"fee_rate_pct"`
	Regions          []string `json:"regions,omitempty"`
}

func (h *Handler) aggregatorOperators(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listLinkedOperators(w, r)
	case http.MethodPost:
		h.addOperator(w, r)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listLinkedOperators(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	operators, err := h.listLinkedOperatorsUseCase.Execute(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": operators})
}

func (h *Handler) addOperator(w http.ResponseWriter, r *http.Request) {
	var req addOperatorRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	relationship, err := h.addOperatorUseCase.Execute(r.Context(), application.AddOperatorCommand{
		AggregatorTenantID: tenantID,
		OperatorTenantID:   strings.TrimSpace(req.OperatorTenantID),
		InvitedEmail:       strings.TrimSpace(req.InvitedEmail),
		Types:              toRelationshipTypes(req.Types),
		FeeRatePct:         req.FeeRatePct,
		Regions:            toRegions(req.Regions),
	})
	if err != nil {
		respondAggregatorError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"status": true, "message": "success", "data": relationship})
}

// aggregatorOperatorByPath handles /dispatch/aggregator/operators/{id}/remove.
func (h *Handler) aggregatorOperatorByPath(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/dispatch/aggregator/operators/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "remove" {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	relationship, err := h.removeOperatorUseCase.Execute(r.Context(), application.RemoveOperatorCommand{
		AggregatorTenantID: tenantID,
		RelationshipID:     parts[0],
		Reason:             req.Reason,
	})
	if err != nil {
		respondAggregatorError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": relationship})
}

// aggregatorInviteByPath handles /dispatch/aggregator/invites/{id}/accept.
func (h *Handler) aggregatorInviteByPath(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/dispatch/aggregator/invites/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "accept" {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil || strings.TrimSpace(claims.TenantID) == "" {
		respondError(w, http.StatusUnauthorized, "authenticated tenant context is required")
		return
	}

	relationship, err := h.acceptAggregatorInviteUseCase.Execute(r.Context(), application.AcceptAggregatorInviteCommand{
		RelationshipID:        parts[0],
		AcceptingTenantID:     claims.TenantID,
		AcceptingAccountEmail: claims.Email,
	})
	if err != nil {
		respondAggregatorError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": relationship})
}

func (h *Handler) aggregatorSubDispatchQueue(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	queue, err := h.subDispatchQueueUseCase.Execute(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": queue})
}

type assignSubDispatchJobRequest struct {
	OperatorTenantID string  `json:"operator_tenant_id"`
	OfferedRateNgn   float64 `json:"offered_rate_ngn"`
}

// aggregatorSubDispatchByPath handles /dispatch/aggregator/sub-dispatch/{jobId}/assign.
func (h *Handler) aggregatorSubDispatchByPath(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/dispatch/aggregator/sub-dispatch/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "assign" {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	var req assignSubDispatchJobRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	offer, err := h.assignSubDispatchJobUseCase.Execute(r.Context(), application.AssignSubDispatchJobCommand{
		AggregatorTenantID: tenantID,
		JobID:              parts[0],
		OperatorTenantID:   strings.TrimSpace(req.OperatorTenantID),
		OfferedRateNgn:     req.OfferedRateNgn,
	})
	if err != nil {
		respondAggregatorError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"status": true, "message": "success", "data": offer})
}

func (h *Handler) aggregatorPerformance(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	performance, err := h.operatorPerformanceUseCase.Execute(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": performance})
}

func (h *Handler) aggregatorComplianceSnapshot(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	snapshot, err := h.complianceSnapshotUseCase.Execute(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": snapshot})
}

func (h *Handler) aggregatorTopOperators(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	ranking, err := h.topPerformingOperatorsUseCase.Execute(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": ranking})
}

type rateOperatorRequest struct {
	OperatorTenantID string `json:"operator_tenant_id"`
	JobID            string `json:"job_id"`
	Value            int    `json:"value"`
	Note             string `json:"note,omitempty"`
}

func (h *Handler) aggregatorRatings(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req rateOperatorRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	rating, err := h.rateOperatorUseCase.Execute(r.Context(), application.RateOperatorCommand{
		RaterTenantID:    tenantID,
		OperatorTenantID: strings.TrimSpace(req.OperatorTenantID),
		JobID:            strings.TrimSpace(req.JobID),
		Value:            req.Value,
		Note:             req.Note,
	})
	if err != nil {
		respondAggregatorError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"status": true, "message": "success", "data": rating})
}

func (h *Handler) aggregatorSettlements(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	result, err := h.listSettlementsUseCase.Execute(r.Context(), tenantID, parsePositiveIntOrZero(q.Get("limit")), parsePositiveIntOrZero(q.Get("offset")))
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": result})
}

type paySettlementRequest struct {
	PaymentReference string `json:"payment_reference"`
}

// aggregatorSettlementByPath handles /dispatch/aggregator/settlements/{id}/pay.
func (h *Handler) aggregatorSettlementByPath(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/dispatch/aggregator/settlements/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "pay" {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	var req paySettlementRequest
	if err := decodeJSONBody(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	claims, _ := httpx.ClaimsFromRequest(r)
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	paidBy := ""
	if claims != nil {
		paidBy = firstNonEmptyDispatchValue(claims.Subject, claims.Email)
	}

	settlement, err := h.paySettlementUseCase.Execute(r.Context(), application.PaySettlementCommand{
		AggregatorTenantID: tenantID,
		SettlementID:       parts[0],
		PaymentReference:   strings.TrimSpace(req.PaymentReference),
		PaidBy:             paidBy,
	})
	if err != nil {
		respondAggregatorError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": settlement})
}

func (h *Handler) aggregatorActivity(w http.ResponseWriter, r *http.Request) {
	if !h.aggregatorConfigured() {
		h.aggregatorUnconfigured(w)
		return
	}
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}
	activity, err := h.listAggregatorActivityUseCase.Execute(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": activity})
}

func respondAggregatorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrRelationshipNotFound),
		errors.Is(err, application.ErrSubDispatchJobNotFound),
		errors.Is(err, application.ErrSettlementNotFound),
		errors.Is(err, application.ErrJobOfferNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, application.ErrRelationshipNotYours),
		errors.Is(err, application.ErrSettlementNotYours),
		errors.Is(err, application.ErrInvitedEmailMismatch),
		errors.Is(err, application.ErrSubDispatchRelationshipNotActive),
		errors.Is(err, application.ErrJobNotSubDispatched):
		respondError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, application.ErrRelationshipAlreadyExists),
		errors.Is(err, application.ErrOperatorHasActiveJobs),
		errors.Is(err, application.ErrRatingAlreadyExists),
		errors.Is(err, application.ErrJobNotDelivered):
		respondError(w, http.StatusConflict, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

func toRelationshipTypes(values []string) []domain.RelationshipType {
	types := make([]domain.RelationshipType, 0, len(values))
	for _, v := range values {
		types = append(types, domain.RelationshipType(strings.TrimSpace(v)))
	}
	return types
}

func toRegions(values []string) []domain.Region {
	regions := make([]domain.Region, 0, len(values))
	for _, v := range values {
		regions = append(regions, domain.Region(strings.TrimSpace(v)))
	}
	return regions
}
