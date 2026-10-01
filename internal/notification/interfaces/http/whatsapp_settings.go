package notificationhttp

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/activitylog"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

type connectWhatsAppSettingsRequest struct {
	PhoneNumberID      string `json:"phone_number_id,omitempty"`
	PhoneNumberIDAlt   string `json:"phoneNumberId,omitempty"`
	BusinessID         string `json:"business_id,omitempty"`
	BusinessIDAlt      string `json:"businessId,omitempty"`
	BusinessAccountID  string `json:"business_account_id,omitempty"`
	BusinessAccountAlt string `json:"businessAccountId,omitempty"`
	Code               string `json:"code,omitempty"`
}

func (h *Handler) whatsAppSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.connectWhatsAppSettings(w, r)
	case http.MethodGet:
		h.getWhatsAppSettings(w, r)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) whatsAppSettingsByPath(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/notify/whatsapp/settings/"), "/")
	if trimmed == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodDelete {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.disconnectWhatsAppSettings(w, r, trimmed)
}

func (h *Handler) connectWhatsAppSettings(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedWhatsAppSettingsTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, whatsAppSettingsAuthMessage(authStatus))
		return
	}
	h.connectWhatsAppAccountFor(w, r, tenantID)
}

func (h *Handler) connectWhatsAppAccountFor(w http.ResponseWriter, r *http.Request, tenantID string) {
	var req connectWhatsAppSettingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	account, err := h.createWhatsAppAccountUseCase.Execute(r.Context(), application.ConnectWhatsAppAccountCommand{
		TenantID:          tenantID,
		PhoneNumberID:     firstNonEmpty(req.PhoneNumberID, req.PhoneNumberIDAlt),
		BusinessAccountID: firstNonEmpty(req.BusinessID, req.BusinessIDAlt, req.BusinessAccountID, req.BusinessAccountAlt),
		Code:              req.Code,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", account)
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "notification",
		Action:     "whatsapp.account.connected",
		EntityType: "whatsapp_account",
		EntityID:   account.ID,
		Summary:    "WhatsApp account connected",
		Metadata: map[string]any{
			"phone_number_id":     account.PhoneNumberID,
			"business_account_id": account.BusinessAccountID,
			"display_phone":       account.DisplayPhoneNumber,
			"business_name":       account.BusinessName,
		},
	})
}

func (h *Handler) getWhatsAppSettings(w http.ResponseWriter, r *http.Request) {
	tenantID, authStatus, ok := authenticatedWhatsAppSettingsTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, whatsAppSettingsAuthMessage(authStatus))
		return
	}
	h.getWhatsAppAccountFor(w, r, tenantID)
}

func (h *Handler) getWhatsAppAccountFor(w http.ResponseWriter, r *http.Request, tenantID string) {
	account, found, err := h.getWhatsAppAccountUseCase.Execute(r.Context(), application.GetWhatsAppAccountQuery{TenantID: tenantID})
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, application.ErrWhatsAppAccountNotFound.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", account)
}

func (h *Handler) disconnectWhatsAppSettings(w http.ResponseWriter, r *http.Request, phoneNumberID string) {
	tenantID, authStatus, ok := authenticatedWhatsAppSettingsTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, whatsAppSettingsAuthMessage(authStatus))
		return
	}
	h.disconnectWhatsAppAccountFor(w, r, tenantID, phoneNumberID)
}

func (h *Handler) disconnectWhatsAppAccountFor(w http.ResponseWriter, r *http.Request, tenantID, phoneNumberID string) {
	err := h.disconnectWhatsAppAccountUseCase.Execute(r.Context(), application.DisconnectWhatsAppAccountCommand{
		TenantID:      tenantID,
		PhoneNumberID: strings.TrimSpace(phoneNumberID),
	})
	if err != nil {
		status := http.StatusBadRequest
		if err == application.ErrWhatsAppAccountNotFound {
			status = http.StatusNotFound
		}
		httpx.WriteError(w, status, err.Error())
		return
	}

	httpx.WriteSuccess(w, http.StatusOK, "success", map[string]any{"phone_number_id": strings.TrimSpace(phoneNumberID)})
	h.publishActivity(r, activitylog.PublishCommand{
		TenantID:   tenantID,
		Service:    "notification",
		Action:     "whatsapp.account.disconnected",
		EntityType: "whatsapp_account",
		EntityID:   strings.TrimSpace(phoneNumberID),
		Summary:    "WhatsApp account disconnected",
		Metadata: map[string]any{
			"phone_number_id": strings.TrimSpace(phoneNumberID),
		},
	})
}

func authenticatedWhatsAppSettingsTenantID(r *http.Request) (string, int, bool) {
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil || strings.TrimSpace(claims.TenantID) == "" {
		return "", http.StatusUnauthorized, false
	}
	if !isWhatsAppSettingsCapableRole(claims.Role) {
		return "", http.StatusForbidden, false
	}
	return strings.TrimSpace(claims.TenantID), http.StatusOK, true
}

func isWhatsAppSettingsCapableRole(role string) bool {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case "PLATFORM_ADMIN", "ENTERPRISE_ADMIN", "AGGREGATOR_ADMIN", "OWNER_OPERATOR", "FLEET_ADMIN", "OPERATIONS_MANAGER":
		return true
	default:
		return false
	}
}

func whatsAppSettingsAuthMessage(status int) string {
	if status == http.StatusForbidden {
		return "administrator role required"
	}
	return "authentication required"
}
