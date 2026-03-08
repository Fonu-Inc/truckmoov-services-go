package notificationhttp

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/application"
)

type sendRequest struct {
	TenantID  string `json:"tenant_id"`
	Channel   string `json:"channel"`
	Recipient string `json:"recipient"`
	Content   string `json:"content"`
}

type Handler struct {
	sendMessageUseCase  *application.SendMessageUseCase
	retryMessageUseCase *application.RetryMessageUseCase
}

func NewHandler(
	sendMessageUseCase *application.SendMessageUseCase,
	retryMessageUseCase *application.RetryMessageUseCase,
) *Handler {
	return &Handler{
		sendMessageUseCase:  sendMessageUseCase,
		retryMessageUseCase: retryMessageUseCase,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/notify/health", h.health)
	mux.HandleFunc("/notify/messages/send", h.sendMessage)
	mux.HandleFunc("/notify/messages/retry/", h.retryMessage)
	mux.HandleFunc("/health", h.health)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "notification"})
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	message, err := h.sendMessageUseCase.Execute(r.Context(), application.SendMessageCommand{
		TenantID:  req.TenantID,
		Channel:   req.Channel,
		Recipient: req.Recipient,
		Content:   req.Content,
	})

	responseCode := http.StatusAccepted
	if err != nil {
		responseCode = http.StatusBadGateway
	}
	respondJSON(w, responseCode, message)
}

func (h *Handler) retryMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/notify/messages/retry/")
	if id == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "missing message id"})
		return
	}

	message, err := h.retryMessageUseCase.Execute(r.Context(), id)
	if err == application.ErrMessageNotFound {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	if err != nil {
		respondJSON(w, http.StatusBadGateway, message)
		return
	}
	respondJSON(w, http.StatusOK, message)
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
