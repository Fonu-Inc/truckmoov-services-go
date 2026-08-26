package notificationhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
)

type createCampaignRequest struct {
	Name         string            `json:"name"`
	Channels     []string          `json:"channels"`
	Bodies       map[string]string `json:"bodies"`
	TemplateName string            `json:"template_name,omitempty"`
	ScheduledFor string            `json:"scheduled_for,omitempty"`
	Recipients   []struct {
		DriverID    string `json:"driver_id"`
		Channel     string `json:"channel"`
		Destination string `json:"destination"`
	} `json:"recipients"`
}

type suppressionRequest struct {
	Destination string `json:"destination"`
	Reason      string `json:"reason,omitempty"`
}

func (h *Handler) campaigns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listCampaigns(w, r)
	case http.MethodPost:
		h.createCampaign(w, r)
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) listCampaigns(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		respondEnvelope(w, authStatus, notificationAuthMessage(authStatus), nil)
		return
	}
	if h.listCampaignsUseCase == nil {
		respondEnvelope(w, http.StatusServiceUnavailable, "campaigns are not configured", nil)
		return
	}

	query := r.URL.Query()
	result, err := h.listCampaignsUseCase.Execute(r.Context(), application.ListCampaignsQuery{
		TenantID: tenantID,
		Status:   strings.TrimSpace(query.Get("status")),
		Query:    strings.TrimSpace(query.Get("query")),
		Page:     parsePositiveInt(query.Get("page")),
		Limit:    parsePositiveInt(query.Get("limit")),
	})
	if err != nil {
		respondEnvelope(w, http.StatusBadGateway, err.Error(), nil)
		return
	}

	respondEnvelope(w, http.StatusOK, "success", result)
}

func (h *Handler) createCampaign(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		respondEnvelope(w, authStatus, notificationAuthMessage(authStatus), nil)
		return
	}
	if h.createCampaignUseCase == nil {
		respondEnvelope(w, http.StatusServiceUnavailable, "campaigns are not configured", nil)
		return
	}

	var request createCampaignRequest
	if !decodeCampaignBody(w, r, &request) {
		return
	}

	var scheduledFor *time.Time
	if raw := strings.TrimSpace(request.ScheduledFor); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respondEnvelope(w, http.StatusBadRequest, "scheduled_for must be an RFC3339 timestamp", nil)
			return
		}
		scheduledFor = &parsed
	}

	recipients := make([]application.CampaignRecipientInput, 0, len(request.Recipients))
	for _, recipient := range request.Recipients {
		recipients = append(recipients, application.CampaignRecipientInput{
			DriverID:    recipient.DriverID,
			Channel:     recipient.Channel,
			Destination: recipient.Destination,
		})
	}

	campaign, err := h.createCampaignUseCase.Execute(r.Context(), application.CreateCampaignCommand{
		TenantID:     tenantID,
		Name:         request.Name,
		Channels:     request.Channels,
		Bodies:       request.Bodies,
		TemplateName: request.TemplateName,
		ScheduledFor: scheduledFor,
		Recipients:   recipients,
	})
	if err != nil {
		respondEnvelope(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	respondEnvelope(w, http.StatusCreated, "success", campaign)
}

// campaignByPath handles /notify/campaigns/{id} and /notify/campaigns/{id}/send
// and /notify/campaigns/{id}/retry.
func (h *Handler) campaignByPath(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		respondEnvelope(w, authStatus, notificationAuthMessage(authStatus), nil)
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/notify/campaigns/"), "/")
	if rest == "" {
		respondEnvelope(w, http.StatusNotFound, "campaign not found", nil)
		return
	}

	parts := strings.Split(rest, "/")
	campaignID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		h.getCampaign(w, r, tenantID, campaignID)
	case (action == "send" || action == "retry") && r.Method == http.MethodPost:
		h.sendCampaign(w, r, tenantID, campaignID, action == "retry")
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) getCampaign(w http.ResponseWriter, r *http.Request, tenantID, campaignID string) {
	if h.campaignRepository == nil {
		respondEnvelope(w, http.StatusServiceUnavailable, "campaigns are not configured", nil)
		return
	}

	campaign, found, err := h.campaignRepository.FindCampaign(r.Context(), tenantID, campaignID)
	if err != nil {
		respondEnvelope(w, http.StatusBadGateway, err.Error(), nil)
		return
	}
	if !found {
		respondEnvelope(w, http.StatusNotFound, "campaign not found", nil)
		return
	}

	recipients, err := h.campaignRepository.ListRecipients(r.Context(), tenantID, campaignID)
	if err != nil {
		respondEnvelope(w, http.StatusBadGateway, err.Error(), nil)
		return
	}

	respondEnvelope(w, http.StatusOK, "success", map[string]any{
		"campaign":   campaign,
		"recipients": recipients,
	})
}

func (h *Handler) sendCampaign(w http.ResponseWriter, r *http.Request, tenantID, campaignID string, retryOnly bool) {
	if h.sendCampaignUseCase == nil {
		respondEnvelope(w, http.StatusServiceUnavailable, "campaigns are not configured", nil)
		return
	}

	result, err := h.sendCampaignUseCase.Execute(r.Context(), application.SendCampaignCommand{
		TenantID:        tenantID,
		CampaignID:      campaignID,
		RetryFailedOnly: retryOnly,
	})
	if errors.Is(err, application.ErrCampaignNotFound) {
		respondEnvelope(w, http.StatusNotFound, "campaign not found", nil)
		return
	}
	if err != nil {
		// A refused wallet is the common case and it is the tenant's to fix,
		// so it reads as a client error rather than a provider fault.
		respondEnvelope(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	respondEnvelope(w, http.StatusOK, "success", result)
}

func (h *Handler) campaignSuppressions(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		respondEnvelope(w, authStatus, notificationAuthMessage(authStatus), nil)
		return
	}
	if h.suppressRecipientUseCase == nil {
		respondEnvelope(w, http.StatusServiceUnavailable, "campaigns are not configured", nil)
		return
	}

	var request suppressionRequest
	if !decodeCampaignBody(w, r, &request) {
		return
	}

	var err error
	switch r.Method {
	case http.MethodPost:
		err = h.suppressRecipientUseCase.Execute(r.Context(), tenantID, request.Destination, request.Reason)
	case http.MethodDelete:
		err = h.suppressRecipientUseCase.Release(r.Context(), tenantID, request.Destination)
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	if err != nil {
		respondEnvelope(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	respondEnvelope(w, http.StatusOK, "success", map[string]any{"destination": request.Destination})
}

func decodeCampaignBody(w http.ResponseWriter, r *http.Request, target any) bool {
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		respondEnvelope(w, http.StatusBadRequest, "invalid request body", nil)
		return false
	}
	if err := json.Unmarshal(payload, target); err != nil {
		respondEnvelope(w, http.StatusBadRequest, "invalid request body", nil)
		return false
	}
	return true
}
