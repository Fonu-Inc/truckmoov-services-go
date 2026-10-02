package notificationhttp

import (
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

const systemCampaignsPrefix = "/notify/system/campaigns/"

type systemCampaignRequest struct {
	Name         string            `json:"name"`
	Channels     []string          `json:"channels"`
	Bodies       map[string]string `json:"bodies"`
	TemplateName string            `json:"template_name"`
	ScheduledFor *time.Time        `json:"scheduled_for"`
	Recipients   []struct {
		AudienceRef string `json:"audience_ref"`
		Channel     string `json:"channel"`
		Destination string `json:"destination"`
	} `json:"recipients"`
}

func (r systemCampaignRequest) command() application.PlatformCampaignCommand {
	recipients := make([]application.CampaignRecipientInput, 0, len(r.Recipients))
	for _, recipient := range r.Recipients {
		recipients = append(recipients, application.CampaignRecipientInput{DriverID: recipient.AudienceRef, Channel: recipient.Channel, Destination: recipient.Destination})
	}
	return application.PlatformCampaignCommand{Name: r.Name, Channels: r.Channels, Bodies: r.Bodies, TemplateName: r.TemplateName, ScheduledFor: r.ScheduledFor, Recipients: recipients}
}

func (h *Handler) campaignTools(w http.ResponseWriter, r *http.Request) *application.PlatformCampaigns {
	if !requirePlatformMessaging(w, r) {
		return nil
	}
	if h.inboxTools == nil || h.inboxTools.Campaigns == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "campaigns are not configured")
		return nil
	}
	return h.inboxTools.Campaigns
}

func (h *Handler) systemCampaigns(w http.ResponseWriter, r *http.Request) {
	campaigns := h.campaignTools(w, r)
	if campaigns == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		list, err := campaigns.List(r.Context(), application.ListCampaignsQuery{
			Status: strings.TrimSpace(query.Get("status")),
			Query:  strings.TrimSpace(query.Get("query")),
			Page:   parsePositiveInt(query.Get("page")),
			Limit:  parsePositiveInt(query.Get("limit")),
		})
		respondInbox(w, http.StatusOK, list, err)
	case http.MethodPost:
		var body systemCampaignRequest
		if !decodeInboxBody(w, r, &body) {
			return
		}
		view, err := campaigns.Create(r.Context(), body.command())
		respondInbox(w, http.StatusCreated, view, err)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) systemCampaignByPath(w http.ResponseWriter, r *http.Request) {
	campaigns := h.campaignTools(w, r)
	if campaigns == nil {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, systemCampaignsPrefix), "/"), "/")
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	switch {
	case parts[0] == "" || len(parts) > 2:
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case action == "" && r.Method == http.MethodGet:
		detail, err := campaigns.Get(r.Context(), parts[0])
		respondInbox(w, http.StatusOK, detail, err)
	case (action == "send" || action == "retry") && r.Method == http.MethodPost:
		result, err := campaigns.Send(r.Context(), parts[0], action == "retry")
		respondInbox(w, http.StatusOK, result, err)
	case action == "cancel" && r.Method == http.MethodPost:
		view, err := campaigns.Cancel(r.Context(), parts[0])
		respondInbox(w, http.StatusOK, view, err)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
