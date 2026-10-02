package notificationhttp

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

const systemDeadLettersPrefix = "/notify/system/dead-letters/"

type deadLetterResponse struct {
	ID              string     `json:"id"`
	OriginalSubject string     `json:"original_subject"`
	TenantID        string     `json:"tenant_id"`
	EventType       string     `json:"event_type"`
	Attempts        int        `json:"attempts"`
	LastError       string     `json:"last_error"`
	FailedAt        time.Time  `json:"failed_at"`
	ReplayedAt      *time.Time `json:"replayed_at,omitempty"`
}

type deadLetterPageResponse struct {
	Items []deadLetterResponse `json:"items"`
	Total int                  `json:"total"`
	Page  int                  `json:"page"`
}

// WithDeadLetters attaches the platform's dead letter list and replay.
func (h *Handler) WithDeadLetters(deadLetters *application.DeadLetters) *Handler {
	h.deadLetters = deadLetters
	return h
}

func (h *Handler) deadLetterTools(w http.ResponseWriter, r *http.Request) *application.DeadLetters {
	if !requirePlatformMessaging(w, r) {
		return nil
	}
	if h.deadLetters == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "dead letters are not configured")
		return nil
	}
	return h.deadLetters
}

func (h *Handler) systemDeadLetters(w http.ResponseWriter, r *http.Request) {
	deadLetters := h.deadLetterTools(w, r)
	if deadLetters == nil {
		return
	}
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page := parsePositiveInt(r.URL.Query().Get("page"))
	letters, total, err := deadLetters.List(r.Context(), page)
	if err != nil {
		respondInboxError(w, err)
		return
	}
	items := make([]deadLetterResponse, 0, len(letters))
	for _, letter := range letters {
		items = append(items, newDeadLetterResponse(letter))
	}
	httpx.WriteOK(w, http.StatusOK, deadLetterPageResponse{Items: items, Total: total, Page: max(page, 1)})
}

func (h *Handler) systemDeadLetterByPath(w http.ResponseWriter, r *http.Request) {
	deadLetters := h.deadLetterTools(w, r)
	if deadLetters == nil {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, systemDeadLettersPrefix), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "replay" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	letter, err := deadLetters.Replay(r.Context(), parts[0])
	switch {
	case errors.Is(err, application.ErrDeadLetterNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, application.ErrDeadLetterAlreadyReplayed):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, application.ErrDeadLetterNotReplayable):
		httpx.WriteError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		httpx.WriteError(w, http.StatusBadGateway, "could not replay the event")
	default:
		httpx.WriteOK(w, http.StatusOK, newDeadLetterResponse(letter))
	}
}

func newDeadLetterResponse(letter application.DeadLetter) deadLetterResponse {
	return deadLetterResponse{
		ID:              letter.ID,
		OriginalSubject: letter.OriginalSubject,
		TenantID:        letter.TenantID,
		EventType:       letter.EventType,
		Attempts:        letter.Attempts,
		LastError:       letter.LastError,
		FailedAt:        letter.FailedAt,
		ReplayedAt:      letter.ReplayedAt,
	}
}
