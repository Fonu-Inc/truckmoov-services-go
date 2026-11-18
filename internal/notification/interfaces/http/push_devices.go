package notificationhttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type pushDeviceRequest struct {
	Token      string `json:"token"`
	Platform   string `json:"platform"`
	AppVersion string `json:"appVersion"`
}

type pushDeviceDeleteRequest struct {
	Token string `json:"token"`
}

func (h *Handler) WithPushDevices(devices *application.PushDevicesUseCase) *Handler {
	h.pushDevices = devices
	return h
}

// pushDevicesRoute binds a token to the signed-in user taken from the token, never
// from the body.
func (h *Handler) pushDevicesRoute(w http.ResponseWriter, r *http.Request) {
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims.Subject == "" || claims.TenantID == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.pushDevices == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "push devices are not configured")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	var err error
	switch r.Method {
	case http.MethodPut:
		var request pushDeviceRequest
		if decoder.Decode(&request) != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		err = h.pushDevices.Register(r.Context(), domain.PushDevice{
			Token:      request.Token,
			TenantID:   claims.TenantID,
			UserID:     claims.Subject,
			Platform:   request.Platform,
			AppVersion: request.AppVersion,
		})
	case http.MethodDelete:
		var request pushDeviceDeleteRequest
		if decoder.Decode(&request) != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		err = h.pushDevices.Unregister(r.Context(), claims.TenantID, claims.Subject, request.Token)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if errors.Is(err, domain.ErrInvalidPushDevice) {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not save this device")
		return
	}
	httpx.WriteOK(w, http.StatusOK, nil)
}
