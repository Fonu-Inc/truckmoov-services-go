package notificationhttp

import (
	"net/http"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

const systemVoicePrefix = "/notify/system/voice/"

type ivrPromptRequest struct {
	Name         string            `json:"name"`
	Category     string            `json:"category"`
	Translations map[string]string `json:"translations"`
	KeyPress     string            `json:"key_press"`
	IsActive     *bool             `json:"is_active"`
}

type initiateCallRequest struct {
	To          string `json:"to"`
	ContactName string `json:"contact_name"`
	Language    string `json:"language"`
	Message     string `json:"message"`
	Record      bool   `json:"record"`
}

type callNotesRequest struct {
	Notes string `json:"notes"`
}

func (h *Handler) systemVoice(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	if h.inboxTools == nil || h.inboxTools.Voice == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "voice is not configured")
		return
	}
	voice := h.inboxTools.Voice
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, systemVoicePrefix), "/"), "/")
	switch parts[0] {
	case "status":
		h.voiceStatus(w, r, voice)
	case "stats":
		h.voiceStats(w, r, voice)
	case "prompts":
		h.voicePrompts(w, r, voice, parts[1:])
	case "calls":
		h.voiceCalls(w, r, voice, parts[1:])
	default:
		httpx.WriteError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) voiceStatus(w http.ResponseWriter, r *http.Request, voice *application.PlatformVoice) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	inboxJSON(w, http.StatusOK, voice.Status())
}

func (h *Handler) voiceStats(w http.ResponseWriter, r *http.Request, voice *application.PlatformVoice) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	stats, err := voice.Stats(r.Context(), parsePositiveInt(r.URL.Query().Get("days")))
	respondInbox(w, http.StatusOK, stats, err)
}

func (h *Handler) voicePrompts(w http.ResponseWriter, r *http.Request, voice *application.PlatformVoice, rest []string) {
	id := ""
	if len(rest) > 0 {
		id = rest[0]
	}
	switch {
	case id == "" && r.Method == http.MethodGet:
		prompts, err := voice.ListPrompts(r.Context())
		respondInbox(w, http.StatusOK, map[string]any{"items": prompts}, err)
	case id == "" && r.Method == http.MethodPost:
		h.savePrompt(w, r, voice, "", http.StatusCreated)
	case id != "" && len(rest) == 1 && r.Method == http.MethodPatch:
		h.savePrompt(w, r, voice, id, http.StatusOK)
	case id != "" && len(rest) == 1 && r.Method == http.MethodDelete:
		if err := voice.DeletePrompt(r.Context(), id); err != nil {
			respondInboxError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent) // contract:raw 204 has no body
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) savePrompt(w http.ResponseWriter, r *http.Request, voice *application.PlatformVoice, id string, status int) {
	var body ivrPromptRequest
	if !decodeInboxBody(w, r, &body) {
		return
	}
	prompt, err := voice.SavePrompt(r.Context(), application.IVRPromptInput{
		ID: id, Name: body.Name, Category: body.Category, Translations: body.Translations, KeyPress: body.KeyPress, IsActive: body.IsActive, ActorID: actorSubject(r),
	})
	respondInbox(w, status, prompt, err)
}

func (h *Handler) voiceCalls(w http.ResponseWriter, r *http.Request, voice *application.PlatformVoice, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		query := r.URL.Query()
		list, err := voice.ListCalls(r.Context(), domain.VoiceCallFilter{
			Direction:    strings.TrimSpace(query.Get("direction")),
			Status:       strings.TrimSpace(query.Get("status")),
			Query:        query.Get("query"),
			HasRecording: query.Get("recording") == "true",
			Page:         parsePositiveInt(query.Get("page")),
			Limit:        parsePositiveInt(query.Get("limit")),
		})
		respondInbox(w, http.StatusOK, list, err)
	case len(rest) == 0 && r.Method == http.MethodPost:
		var body initiateCallRequest
		if !decodeInboxBody(w, r, &body) {
			return
		}
		call, err := voice.Initiate(r.Context(), application.InitiateCallCommand{
			To: body.To, ContactName: body.ContactName, Language: body.Language, Message: body.Message, Record: body.Record, ActorID: actorSubject(r),
		})
		respondInbox(w, http.StatusCreated, call, err)
	case len(rest) == 1 && r.Method == http.MethodGet:
		call, err := voice.GetCall(r.Context(), rest[0])
		respondInbox(w, http.StatusOK, call, err)
	case len(rest) == 1 && r.Method == http.MethodPatch:
		var body callNotesRequest
		if !decodeInboxBody(w, r, &body) {
			return
		}
		call, err := voice.UpdateNotes(r.Context(), rest[0], body.Notes)
		respondInbox(w, http.StatusOK, call, err)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
