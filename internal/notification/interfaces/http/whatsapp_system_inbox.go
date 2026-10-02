package notificationhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

const (
	systemConversationsPrefix = "/notify/system/whatsapp/conversations/"
	maxInboxBodyBytes         = 1 << 20
)

// WithPlatformInbox attaches the staff inbox reads. A builder, like the other
// optional features: the routes answer unavailable until it is called.
func (h *Handler) WithPlatformInbox(queries *application.PlatformInboxQueries) *Handler {
	h.platformInbox = queries
	return h
}

func (h *Handler) systemWhatsAppConversations(w http.ResponseWriter, r *http.Request) {
	if !h.allowInboxRead(w, r) {
		return
	}
	values := r.URL.Query()
	assignee := strings.TrimSpace(values.Get("assignee"))
	if assignee == "me" {
		assignee = actorSubject(r)
	}
	page, err := h.platformInbox.ListConversations(r.Context(), application.ListInboxQuery{
		Query:    values.Get("query"),
		State:    values.Get("state"),
		Status:   values.Get("status"),
		Assignee: assignee,
		Tag:      values.Get("tag"),
		Cursor:   values.Get("cursor"),
		Limit:    parseLimit(values.Get("limit")),
	})
	if err != nil {
		respondInboxError(w, err)
		return
	}
	inboxJSON(w, http.StatusOK, page)
}

func (h *Handler) systemWhatsAppConversationByPath(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	if h.platformInbox == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "platform inbox is not configured")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, systemConversationsPrefix), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "unread-summary":
		h.systemWhatsAppUnreadSummary(w, r)
	case len(parts) == 1 && parts[0] == "bulk":
		h.systemWhatsAppBulk(w, r)
	case len(parts) == 1 && parts[0] != "":
		h.systemWhatsAppConversation(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "messages":
		h.systemWhatsAppMessages(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "notes":
		h.systemWhatsAppNote(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "media":
		h.systemWhatsAppSendMedia(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "links":
		h.systemWhatsAppLinks(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "scheduled":
		h.systemWhatsAppSchedule(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "messages":
		h.systemWhatsAppCancelScheduled(w, r, parts[2])
	case len(parts) == 4 && parts[1] == "messages" && parts[3] == "retry":
		h.systemWhatsAppRetry(w, r, parts[2])
	default:
		httpx.WriteError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) systemWhatsAppUnreadSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	summary, err := h.platformInbox.UnreadSummary(r.Context())
	respondInbox(w, http.StatusOK, summary, err)
}

func (h *Handler) systemWhatsAppConversation(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		conversation, err := h.platformInbox.GetConversation(r.Context(), id)
		respondInbox(w, http.StatusOK, conversation, err)
	case http.MethodPatch:
		h.systemWhatsAppUpdateConversation(w, r, id)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) systemWhatsAppMessages(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	values := r.URL.Query()
	page, err := h.platformInbox.ListMessages(r.Context(), id, values.Get("before"), parseLimit(values.Get("limit")))
	respondInbox(w, http.StatusOK, page, err)
}

func (h *Handler) allowInboxRead(w http.ResponseWriter, r *http.Request) bool {
	if !requirePlatformMessaging(w, r) {
		return false
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if h.platformInbox == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "platform inbox is not configured")
		return false
	}
	return true
}

func parseLimit(raw string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit < 0 {
		return 0
	}
	return limit
}

func inboxJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func respondInbox(w http.ResponseWriter, status int, body any, err error) {
	if err != nil {
		respondInboxError(w, err)
		return
	}
	inboxJSON(w, status, body)
}

func decodeInboxBody(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInboxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func respondInboxError(w http.ResponseWriter, err error) {
	var input application.InboxInputError
	switch {
	case isNotFound(err):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, application.ErrInvalidInboxCursor):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &input):
		httpx.WriteError(w, http.StatusBadRequest, input.Error())
	case errors.Is(err, application.ErrWhatsAppConversationLocked):
		inboxJSON(w, http.StatusConflict, map[string]string{"code": application.ErrorCodeWindowLocked, "message": "The 24-hour window has closed. Send an approved template to reopen it."})
	case errors.Is(err, application.ErrSMSTemplateNameTaken), errors.Is(err, application.ErrCannedReplyShortcutTaken):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, application.ErrOutreachPhoneInvalid):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, application.ErrVoiceNotConfigured):
		httpx.WriteError(w, http.StatusServiceUnavailable, err.Error())
	default:
		respondInboxUpstream(w, err)
	}
}

func isNotFound(err error) bool {
	for _, missing := range []error{
		application.ErrWhatsAppConversationNotFound,
		application.ErrPlatformMediaNotFound,
		application.ErrCannedReplyNotFound,
		application.ErrCampaignNotFound,
		application.ErrSMSTemplateNotFound,
		application.ErrVoicePromptNotFound,
		application.ErrVoiceCallNotFound,
	} {
		if errors.Is(err, missing) {
			return true
		}
	}
	return false
}

func respondInboxUpstream(w http.ResponseWriter, err error) {
	if _, isMeta := domain.AsWhatsAppAPIError(err); isMeta {
		httpx.WriteError(w, http.StatusBadGateway, domain.PlainErrorText(err))
		return
	}
	httpx.WriteError(w, http.StatusBadGateway, "could not complete the request")
}
