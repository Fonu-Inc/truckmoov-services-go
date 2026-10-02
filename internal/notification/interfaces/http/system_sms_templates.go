package notificationhttp

import (
	"net/http"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

const systemSMSTemplatesPrefix = "/notify/system/sms-templates/"

type smsTemplateRequest struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Content  string `json:"content"`
	IsActive *bool  `json:"is_active"`
}

type smsTemplateTestRequest struct {
	To     string            `json:"to"`
	Values map[string]string `json:"values"`
}

func (h *Handler) smsTemplateTools(w http.ResponseWriter, r *http.Request) *application.PlatformSMSTemplates {
	if !requirePlatformMessaging(w, r) {
		return nil
	}
	if h.inboxTools == nil || h.inboxTools.SMS == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "sms templates are not configured")
		return nil
	}
	return h.inboxTools.SMS
}

func (h *Handler) systemSMSTemplates(w http.ResponseWriter, r *http.Request) {
	templates := h.smsTemplateTools(w, r)
	if templates == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := templates.List(r.Context())
		respondInbox(w, http.StatusOK, map[string]any{"items": items}, err)
	case http.MethodPost:
		h.saveSMSTemplate(w, r, templates, "")
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) systemSMSTemplateByPath(w http.ResponseWriter, r *http.Request) {
	templates := h.smsTemplateTools(w, r)
	if templates == nil {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, systemSMSTemplatesPrefix), "/"), "/")
	switch {
	case parts[0] == "" || len(parts) > 2:
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case len(parts) == 2 && parts[1] == "test" && r.Method == http.MethodPost:
		var body smsTemplateTestRequest
		if !decodeInboxBody(w, r, &body) {
			return
		}
		result, err := templates.Test(r.Context(), application.SMSTemplateTest{TemplateID: parts[0], To: body.To, Values: body.Values})
		respondInbox(w, http.StatusOK, result, err)
	case len(parts) == 1 && r.Method == http.MethodPatch:
		h.saveSMSTemplate(w, r, templates, parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := templates.Delete(r.Context(), parts[0]); err != nil {
			respondInboxError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent) // contract:raw 204 has no body
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) saveSMSTemplate(w http.ResponseWriter, r *http.Request, templates *application.PlatformSMSTemplates, id string) {
	var body smsTemplateRequest
	if !decodeInboxBody(w, r, &body) {
		return
	}
	status := http.StatusCreated
	if id != "" {
		status = http.StatusOK
	}
	template, err := templates.Save(r.Context(), application.SMSTemplateInput{ID: id, Name: body.Name, Category: body.Category, Content: body.Content, IsActive: body.IsActive, ActorID: actorSubject(r)})
	respondInbox(w, status, template, err)
}
