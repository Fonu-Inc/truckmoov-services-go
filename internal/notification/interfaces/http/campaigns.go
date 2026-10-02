package notificationhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
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
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listCampaigns(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}
	if h.listCampaignsUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "campaigns are not configured")
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
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", result)
}

func (h *Handler) createCampaign(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}
	if h.createCampaignUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "campaigns are not configured")
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
			httpx.WriteError(w, http.StatusBadRequest, "scheduled_for must be an RFC3339 timestamp")
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
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusCreated, "success", campaign)
}

// campaignByPath handles /notify/campaigns/{id} and /notify/campaigns/{id}/send
// and /notify/campaigns/{id}/retry, which takes ?scope=interrupted to resend
// only to recipients a dead run left unconfirmed.
func (h *Handler) campaignByPath(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/notify/campaigns/"), "/")
	if rest == "" {
		httpx.WriteError(w, http.StatusNotFound, "campaign not found")
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
		h.sendCampaign(w, r, tenantID, campaignID, retryScope(action, r))
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) getCampaign(w http.ResponseWriter, r *http.Request, tenantID, campaignID string) {
	if h.campaignRepository == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "campaigns are not configured")
		return
	}

	campaign, found, err := h.campaignRepository.FindCampaign(r.Context(), tenantID, campaignID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, "campaign not found")
		return
	}

	recipients, err := h.campaignRepository.ListRecipients(r.Context(), tenantID, campaignID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", map[string]any{
		"campaign":   campaign,
		"recipients": recipients,
	})
}

type campaignRun struct {
	retryFailedOnly bool
	interruptedOnly bool
}

// retryScope reads what a retry covers: every failed recipient, or with
// ?scope=interrupted only those a dead run left unconfirmed.
func retryScope(action string, r *http.Request) campaignRun {
	return campaignRun{
		retryFailedOnly: action == "retry",
		interruptedOnly: action == "retry" && r.URL.Query().Get("scope") == "interrupted",
	}
}

func (h *Handler) sendCampaign(w http.ResponseWriter, r *http.Request, tenantID, campaignID string, run campaignRun) {
	if h.sendCampaignUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "campaigns are not configured")
		return
	}

	result, err := h.sendCampaignUseCase.Execute(r.Context(), application.SendCampaignCommand{
		TenantID:        tenantID,
		CampaignID:      campaignID,
		RetryFailedOnly: run.retryFailedOnly,
		InterruptedOnly: run.interruptedOnly,
	})
	if errors.Is(err, application.ErrCampaignNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "campaign not found")
		return
	}
	if errors.Is(err, application.ErrCampaignAlreadyRunning) {
		httpx.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		// A refused wallet is the common case and it is the tenant's to fix,
		// so it reads as a client error rather than a provider fault.
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", result)
}

func (h *Handler) campaignSuppressions(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, notificationAuthMessage(authStatus))
		return
	}
	if h.suppressRecipientUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "campaigns are not configured")
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
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, "success", map[string]any{"destination": request.Destination})
}

func decodeCampaignBody(w http.ResponseWriter, r *http.Request, target any) bool {
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if err := json.Unmarshal(payload, target); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}
