package notificationhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/activitylog"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

const systemWhatsAppTemplatesPrefix = "/notify/system/whatsapp/templates/"

// WithWhatsAppConnector attaches the number health actions: refresh,
// resubscribe and register. Without it those routes answer unavailable and
// reads return the stored account.
func (h *Handler) WithWhatsAppConnector(connector *application.WhatsAppConnector) *Handler {
	h.whatsAppConnector = connector
	return h
}

// WithPlatformOutreach attaches template sends from the platform number and
// template deletion.
func (h *Handler) WithPlatformOutreach(outreach *application.PlatformTemplateSendUseCase, deleteTemplate *application.DeleteWhatsAppTemplateUseCase) *Handler {
	h.platformOutreach = outreach
	h.deleteWhatsAppTemplateUseCase = deleteTemplate
	return h
}

func connectErrorStatus(err error) int {
	switch {
	case errors.Is(err, domain.ErrWhatsAppNumberInUse):
		return http.StatusConflict
	case errors.Is(err, application.ErrWhatsAppAccountNotFound):
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}

func (h *Handler) refreshedWhatsAppAccount(r *http.Request, account domain.WhatsAppAccount) domain.WhatsAppAccount {
	if h.whatsAppConnector == nil {
		return account
	}
	if r.URL.Query().Get("refresh") == "true" {
		if refreshed, err := h.whatsAppConnector.Refresh(r.Context(), account.TenantID); err == nil {
			return refreshed
		}
		return account
	}
	return h.whatsAppConnector.RefreshIfStale(r.Context(), account)
}

func isWhatsAppConnectorAction(segment string) bool {
	switch segment {
	case "refresh", "resubscribe", "register":
		return true
	}
	return false
}

func (h *Handler) systemWhatsAppConnectorAction(w http.ResponseWriter, r *http.Request, action string) {
	if h.whatsAppConnector == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "whatsapp connector is not configured")
		return
	}
	var account domain.WhatsAppAccount
	var err error
	switch action {
	case "refresh":
		account, err = h.whatsAppConnector.Refresh(r.Context(), identity.SystemTenantID)
	case "resubscribe":
		account, err = h.whatsAppConnector.Resubscribe(r.Context(), identity.SystemTenantID)
	default:
		account, err = h.whatsAppConnector.Register(r.Context(), identity.SystemTenantID)
	}
	if err != nil {
		httpx.WriteError(w, whatsAppConnectorStatus(err), domain.PlainErrorText(err))
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, "success", account)
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   identity.SystemTenantID,
		Service:    "notification",
		Action:     "whatsapp.account." + action,
		EntityType: "whatsapp_account",
		EntityID:   account.ID,
		Summary:    "WhatsApp account " + action,
		Metadata:   map[string]any{"phone_number_id": account.PhoneNumberID},
	})
}

func whatsAppConnectorStatus(err error) int {
	if errors.Is(err, application.ErrWhatsAppAccountNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

func (h *Handler) systemWhatsAppTemplateByPath(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, systemWhatsAppTemplatesPrefix), "/")
	if name == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodDelete {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.deleteWhatsAppTemplateUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "template deletion is not configured")
		return
	}
	if err := h.deleteWhatsAppTemplateUseCase.Execute(r.Context(), identity.SystemTenantID, name); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, domain.PlainErrorText(err))
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, "success", map[string]any{"name": name})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   identity.SystemTenantID,
		Service:    "notification",
		Action:     "whatsapp.template.deleted",
		EntityType: "whatsapp_template",
		EntityID:   name,
		Summary:    "WhatsApp template deleted",
		Metadata:   map[string]any{"name": name},
	})
}

type outreachRequest struct {
	ClientMessageID string            `json:"client_message_id,omitempty"`
	ConversationID  string            `json:"conversation_id,omitempty"`
	Phone           string            `json:"phone,omitempty"`
	Name            string            `json:"name,omitempty"`
	TemplateName    string            `json:"template_name"`
	Language        string            `json:"language,omitempty"`
	Variables       map[string]string `json:"variables,omitempty"`
}

// systemWhatsAppOutreach sends an approved template from the platform number,
// which is how staff start a conversation or reopen one past its 24-hour
// window.
func (h *Handler) systemWhatsAppOutreach(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.platformOutreach == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "platform outreach is not configured")
		return
	}
	var req outreachRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.platformOutreach.Execute(r.Context(), application.PlatformTemplateSendCommand{
		ClientMessageID: req.ClientMessageID,
		ConversationID:  req.ConversationID,
		Phone:           req.Phone,
		Name:            req.Name,
		TemplateName:    req.TemplateName,
		Language:        req.Language,
		Variables:       req.Variables,
		ActorID:         actorSubject(r),
	})
	if err != nil {
		httpx.WriteError(w, outreachErrorStatus(err), domain.PlainErrorText(err))
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, "success", result)
}

func outreachErrorStatus(err error) int {
	if errors.Is(err, application.ErrWhatsAppConversationNotFound) {
		return http.StatusNotFound
	}
	if _, isMeta := domain.AsWhatsAppAPIError(err); isMeta {
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}

func actorSubject(r *http.Request) string {
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil {
		return ""
	}
	return strings.TrimSpace(claims.Subject)
}

func parseTemplateListQuery(r *http.Request) (limit int, after string) {
	limit, _ = strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit < 0 || limit > 100 {
		limit = 0
	}
	return limit, strings.TrimSpace(r.URL.Query().Get("after"))
}
