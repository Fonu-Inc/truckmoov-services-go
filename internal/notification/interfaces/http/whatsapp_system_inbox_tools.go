package notificationhttp

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

// PlatformInboxTools are the staff actions beyond reading: owning a
// conversation, notes, attachments, scheduled sends, retries and canned
// replies.
type PlatformInboxTools struct {
	Admin     *application.PlatformInboxAdmin
	Media     *application.PlatformMediaSendUseCase
	MediaGet  *application.PlatformMediaFetchUseCase
	Retry     *application.PlatformRetryUseCase
	Scheduled *application.PlatformScheduledSends
	Canned    *application.CannedReplies
	Links     *application.PlatformContactLinks
	Campaigns *application.PlatformCampaigns
	SMS       *application.PlatformSMSTemplates
	Voice     *application.PlatformVoice
}

func (h *Handler) WithPlatformInboxTools(tools *PlatformInboxTools) *Handler {
	h.inboxTools = tools
	return h
}

func (h *Handler) tools(w http.ResponseWriter) *PlatformInboxTools {
	if h.inboxTools == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "platform inbox tools are not configured")
	}
	return h.inboxTools
}

type conversationUpdateRequest struct {
	Name           *string   `json:"name"`
	Status         *string   `json:"status"`
	Priority       *string   `json:"priority"`
	AssigneeUserID *string   `json:"assignee_user_id"`
	Tags           *[]string `json:"tags"`
}

func (r conversationUpdateRequest) update() application.ConversationUpdate {
	return application.ConversationUpdate{Name: r.Name, Status: r.Status, Priority: r.Priority, AssigneeUserID: r.AssigneeUserID, Tags: r.Tags}
}

func (h *Handler) systemWhatsAppUpdateConversation(w http.ResponseWriter, r *http.Request, id string) {
	tools := h.tools(w)
	if tools == nil {
		return
	}
	var body conversationUpdateRequest
	if !decodeInboxBody(w, r, &body) {
		return
	}
	view, err := tools.Admin.Update(r.Context(), id, body.update())
	respondInbox(w, http.StatusOK, view, err)
}

type bulkRequest struct {
	IDs    []string                  `json:"ids"`
	Action string                    `json:"action"`
	Update conversationUpdateRequest `json:"update"`
}

func (h *Handler) systemWhatsAppBulk(w http.ResponseWriter, r *http.Request) {
	tools := h.tools(w)
	if tools == nil {
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body bulkRequest
	if !decodeInboxBody(w, r, &body) {
		return
	}
	changed, err := tools.Admin.Bulk(r.Context(), body.IDs, application.BulkOperation{Mark: body.Action, Update: body.Update.update()})
	respondInbox(w, http.StatusOK, map[string]int{"changed": changed}, err)
}

type noteRequest struct {
	Text string `json:"text"`
}

func (h *Handler) systemWhatsAppNote(w http.ResponseWriter, r *http.Request, id string) {
	tools := h.tools(w)
	if tools == nil {
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body noteRequest
	if !decodeInboxBody(w, r, &body) {
		return
	}
	view, err := tools.Admin.AddNote(r.Context(), id, actorSubject(r), body.Text)
	respondInbox(w, http.StatusCreated, view, err)
}

func (h *Handler) systemWhatsAppSendMedia(w http.ResponseWriter, r *http.Request, id string) {
	tools := h.tools(w)
	if tools == nil {
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	command, ok := readMediaForm(w, r, id)
	if !ok {
		return
	}
	command.ActorID = actorSubject(r)
	view, err := tools.Media.Execute(r.Context(), command)
	respondInbox(w, http.StatusCreated, view, err)
}

func readMediaForm(w http.ResponseWriter, r *http.Request, conversationID string) (application.PlatformMediaCommand, bool) {
	httpx.LimitBody(w, r, application.MaxPlatformMediaBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "the upload is invalid or too large")
		return application.PlatformMediaCommand{}, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "a file is required")
		return application.PlatformMediaCommand{}, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, application.MaxPlatformMediaBytes+1))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "the file could not be read")
		return application.PlatformMediaCommand{}, false
	}
	return application.PlatformMediaCommand{
		ClientMessageID: strings.TrimSpace(r.FormValue("client_message_id")),
		ConversationID:  conversationID,
		Filename:        header.Filename,
		ContentType:     header.Header.Get("Content-Type"),
		Data:            data,
		Caption:         r.FormValue("caption"),
	}, true
}

type scheduleRequest struct {
	ClientMessageID string    `json:"client_message_id"`
	Text            string    `json:"text"`
	SendAt          time.Time `json:"send_at"`
}

func (h *Handler) systemWhatsAppSchedule(w http.ResponseWriter, r *http.Request, id string) {
	tools := h.tools(w)
	if tools == nil {
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body scheduleRequest
	if !decodeInboxBody(w, r, &body) {
		return
	}
	view, err := tools.Scheduled.Schedule(r.Context(), application.ScheduleCommand{
		ClientMessageID: body.ClientMessageID,
		ConversationID:  id,
		Text:            body.Text,
		SendAt:          body.SendAt,
		ActorID:         actorSubject(r),
	})
	respondInbox(w, http.StatusCreated, view, err)
}

func (h *Handler) systemWhatsAppCancelScheduled(w http.ResponseWriter, r *http.Request, messageID string) {
	tools := h.tools(w)
	if tools == nil {
		return
	}
	if r.Method != http.MethodDelete {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := tools.Scheduled.Cancel(r.Context(), messageID); err != nil {
		respondInboxError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent) // contract:raw 204 has no body
}

func (h *Handler) systemWhatsAppRetry(w http.ResponseWriter, r *http.Request, messageID string) {
	tools := h.tools(w)
	if tools == nil {
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	view, err := tools.Retry.Execute(r.Context(), messageID, actorSubject(r))
	respondInbox(w, http.StatusOK, view, err)
}

func (h *Handler) systemWhatsAppMedia(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	tools := h.tools(w)
	if tools == nil {
		return
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	messageID := strings.Trim(strings.TrimPrefix(r.URL.Path, application.SystemMediaPathPrefix), "/")
	media, err := tools.MediaGet.Execute(r.Context(), messageID)
	if err != nil {
		respondInboxError(w, err)
		return
	}
	writeMedia(w, media)
}

func writeMedia(w http.ResponseWriter, media application.PlatformMedia) {
	contentType := media.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", mediaDisposition(contentType, media.Filename))
	_, _ = w.Write(media.Data) // contract:raw binary media proxy
}

var inlineMediaTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
	"audio/aac": true, "audio/mp4": true, "audio/mpeg": true, "audio/amr": true, "audio/ogg": true,
	"video/mp4": true, "video/3gpp": true,
}

// Only types a browser shows harmlessly are inline; everything else downloads.
func mediaDisposition(contentType, filename string) string {
	kind := "attachment"
	if inlineMediaTypes[strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))] {
		kind = "inline"
	}
	if filename == "" {
		return kind
	}
	return kind + "; filename=" + quoteFilename(filename)
}

func quoteFilename(name string) string {
	return `"` + strings.NewReplacer(`"`, "", "\\", "", "\r", "", "\n", "").Replace(name) + `"`
}

type cannedReplyRequest struct {
	Shortcut string `json:"shortcut"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

func (h *Handler) systemCannedReplies(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	tools := h.tools(w)
	if tools == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		replies, err := tools.Canned.List(r.Context())
		respondInbox(w, http.StatusOK, map[string]any{"items": replies}, err)
	case http.MethodPost:
		h.saveCannedReply(w, r, "")
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) systemCannedReplyByPath(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	tools := h.tools(w)
	if tools == nil {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/notify/system/whatsapp/canned-replies/"), "/")
	switch {
	case id == "":
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case r.Method == http.MethodPatch:
		h.saveCannedReply(w, r, id)
	case r.Method == http.MethodDelete:
		if err := tools.Canned.Delete(r.Context(), id); err != nil {
			respondInboxError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent) // contract:raw 204 has no body
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) saveCannedReply(w http.ResponseWriter, r *http.Request, id string) {
	var body cannedReplyRequest
	if !decodeInboxBody(w, r, &body) {
		return
	}
	status := http.StatusCreated
	if id != "" {
		status = http.StatusOK
	}
	reply, err := h.inboxTools.Canned.Save(r.Context(), application.CannedReplyInput{ID: id, Shortcut: body.Shortcut, Title: body.Title, Body: body.Body, ActorID: actorSubject(r)})
	respondInbox(w, status, reply, err)
}

func (h *Handler) systemWhatsAppLinks(w http.ResponseWriter, r *http.Request, id string) {
	tools := h.tools(w)
	if tools == nil {
		return
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	links, err := tools.Links.Execute(r.Context(), id)
	respondInbox(w, http.StatusOK, links, err)
}
