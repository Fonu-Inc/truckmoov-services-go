package notificationhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

const systemConversationsPrefix = "/notify/system/whatsapp/conversations/"

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
	page, err := h.platformInbox.ListConversations(r.Context(), application.ListInboxQuery{
		Query:  values.Get("query"),
		State:  values.Get("state"),
		Cursor: values.Get("cursor"),
		Limit:  parseLimit(values.Get("limit")),
	})
	if err != nil {
		respondInboxError(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, page)
}

func (h *Handler) systemWhatsAppConversationByPath(w http.ResponseWriter, r *http.Request) {
	if !h.allowInboxRead(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, systemConversationsPrefix), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		h.systemWhatsAppConversation(w, r, parts[0])
	case len(parts) == 2 && parts[0] != "" && parts[1] == "messages":
		h.systemWhatsAppMessages(w, r, parts[0])
	default:
		httpx.WriteError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) systemWhatsAppConversation(w http.ResponseWriter, r *http.Request, id string) {
	conversation, err := h.platformInbox.GetConversation(r.Context(), id)
	if err != nil {
		respondInboxError(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, conversation)
}

func (h *Handler) systemWhatsAppMessages(w http.ResponseWriter, r *http.Request, id string) {
	values := r.URL.Query()
	page, err := h.platformInbox.ListMessages(r.Context(), id, values.Get("before"), parseLimit(values.Get("limit")))
	if err != nil {
		respondInboxError(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, page)
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

func respondInboxError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrWhatsAppConversationNotFound):
		httpx.WriteError(w, http.StatusNotFound, "conversation not found")
	case errors.Is(err, application.ErrInvalidInboxCursor):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteError(w, http.StatusBadGateway, "could not load messages")
	}
}
