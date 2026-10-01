package dispatchhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/application"
)

type createJobOfferRequest struct {
	JobID          string  `json:"job_id"`
	TargetTenantID string  `json:"target_tenant_id"`
	OfferedRateNgn float64 `json:"offered_rate_ngn"`
	ExpiresAt      string  `json:"expires_at"`
}

type respondToJobOfferRequest struct {
	Note string `json:"note,omitempty"`
}

func (h *Handler) jobOffers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listJobOffers(w, r)
	case http.MethodPost:
		h.createJobOffer(w, r)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listJobOffers(w http.ResponseWriter, r *http.Request) {
	if h.listJobOffersUseCase == nil {
		respondError(w, http.StatusServiceUnavailable, "job offers are not configured")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	role := strings.TrimSpace(query.Get("role"))
	if role == "" {
		role = "target"
	}
	if role != "target" && role != "offering" {
		respondError(w, http.StatusBadRequest, "role must be target or offering")
		return
	}

	result, err := h.listJobOffersUseCase.Execute(r.Context(), application.JobOfferQuery{
		TenantID: tenantID,
		Role:     role,
		Status:   strings.TrimSpace(query.Get("status")),
		Page:     parsePositiveIntOrZero(query.Get("page")),
		Limit:    parsePositiveIntOrZero(query.Get("limit")),
	})
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": result})
}

func (h *Handler) createJobOffer(w http.ResponseWriter, r *http.Request) {
	if h.createJobOfferUseCase == nil {
		respondError(w, http.StatusServiceUnavailable, "job offers are not configured")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	var request createJobOfferRequest
	if err := decodeJSONBody(r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(request.ExpiresAt))
	if err != nil {
		respondError(w, http.StatusBadRequest, "expires_at must be an RFC3339 timestamp")
		return
	}

	offer, err := h.createJobOfferUseCase.Execute(r.Context(), application.CreateJobOfferCommand{
		OfferingTenantID: tenantID,
		JobID:            strings.TrimSpace(request.JobID),
		TargetTenantID:   strings.TrimSpace(request.TargetTenantID),
		OfferedRateNgn:   request.OfferedRateNgn,
		ExpiresAt:        expiresAt,
	})
	if err != nil {
		respondJobOfferError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{"status": true, "message": "success", "data": offer})
}

// jobOfferByPath handles /dispatch/job-offers/{id}/accept and .../decline.
func (h *Handler) jobOfferByPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/dispatch/job-offers/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		respondError(w, http.StatusNotFound, "job offer not found")
		return
	}
	offerID, action := parts[0], parts[1]

	var request respondToJobOfferRequest
	if err := decodeJSONBody(r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	command := application.RespondToJobOfferCommand{OfferID: offerID, TenantID: tenantID, Note: request.Note}

	switch action {
	case "accept":
		if h.acceptJobOfferUseCase == nil {
			respondError(w, http.StatusServiceUnavailable, "job offers are not configured")
			return
		}
		result, err := h.acceptJobOfferUseCase.Execute(r.Context(), command)
		if err != nil {
			respondJobOfferError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": result})
	case "decline":
		if h.declineJobOfferUseCase == nil {
			respondError(w, http.StatusServiceUnavailable, "job offers are not configured")
			return
		}
		offer, err := h.declineJobOfferUseCase.Execute(r.Context(), command)
		if err != nil {
			respondJobOfferError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": offer})
	default:
		respondError(w, http.StatusNotFound, "job offer not found")
	}
}

func (h *Handler) jobOfferCapacity(w http.ResponseWriter, r *http.Request) {
	if h.activeJobCapacityUseCase == nil {
		respondError(w, http.StatusServiceUnavailable, "job offers are not configured")
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

	capacity, err := h.activeJobCapacityUseCase.Execute(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": true, "message": "success", "data": capacity})
}

func respondJobOfferError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrJobOfferNotFound), errors.Is(err, application.ErrJobNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, application.ErrNotYourOffer):
		respondError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, application.ErrOverActiveJobCap):
		respondError(w, http.StatusConflict, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

func parsePositiveIntOrZero(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}
