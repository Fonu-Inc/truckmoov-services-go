package notificationhttp

import (
	"encoding/json"
	"net/http"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type notificationPreferencesRequest struct {
	Email    bool `json:"email"`
	WhatsApp bool `json:"whatsapp"`
	SMS      bool `json:"sms"`
}

// WithPreferences attaches the tenant's choice of channels for its own alerts.
func (h *Handler) WithPreferences(preferences *application.NotificationPreferencesUseCase) *Handler {
	h.preferences = preferences
	return h
}

// notificationPreferences reads and writes the caller's own tenant, taken from
// the token and never from the request.
func (h *Handler) notificationPreferences(w http.ResponseWriter, r *http.Request) {
	tenantID := httpx.TenantIDFromRequest(r)
	if tenantID == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.preferences == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "notification preferences are not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getNotificationPreferences(w, r, tenantID)
	case http.MethodPut:
		h.putNotificationPreferences(w, r, tenantID)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) getNotificationPreferences(w http.ResponseWriter, r *http.Request, tenantID string) {
	preferences, err := h.preferences.Get(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not read your notification choices")
		return
	}
	httpx.WriteOK(w, http.StatusOK, preferences)
}

func (h *Handler) putNotificationPreferences(w http.ResponseWriter, r *http.Request, tenantID string) {
	var request notificationPreferencesRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	preferences, err := h.preferences.Update(r.Context(), application.UpdateNotificationPreferencesCommand{
		TenantID: tenantID,
		Email:    request.Email,
		WhatsApp: request.WhatsApp,
		SMS:      request.SMS,
	})
	if err == domain.ErrNoNotificationChannel {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not save your notification choices")
		return
	}
	httpx.WriteOK(w, http.StatusOK, preferences)
}
