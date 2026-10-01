package notificationhttp

import (
	"net/http"
	"strings"

	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

// The platform's own WhatsApp Business number lives under the system tenant, so
// it is stored, encrypted and read exactly like a tenant's, but only platform
// staff holding admin.messaging can reach it.
const systemWhatsAppSettingsPrefix = "/notify/system/whatsapp/settings/"

// Route enforcement can be in log-only mode, and this account speaks for the
// whole platform, so the handler checks the caller itself, as the tenant
// settings do.
func requirePlatformMessaging(w http.ResponseWriter, r *http.Request) bool {
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil {
		respondEnvelope(w, http.StatusUnauthorized, "authentication required", nil)
		return false
	}
	needed := "admin.messaging.manage"
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		needed = "admin.messaging.read"
	}
	held := claims.Permissions
	if held == nil {
		held = identity.RolePermissions(claims.Role, claims.TenantID)
	}
	if !identity.IsPlatformActor(claims.Role, claims.TenantID) || !containsPermission(held, needed) {
		respondEnvelope(w, http.StatusForbidden, "platform messaging permission required", nil)
		return false
	}
	return true
}

func containsPermission(held []string, needed string) bool {
	for _, permission := range held {
		if permission == needed {
			return true
		}
	}
	return false
}

func (h *Handler) systemWhatsAppSettings(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.connectWhatsAppAccountFor(w, r, identity.SystemTenantID)
	case http.MethodGet:
		h.getWhatsAppAccountFor(w, r, identity.SystemTenantID)
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) systemWhatsAppSettingsByPath(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	phoneNumberID := strings.Trim(strings.TrimPrefix(r.URL.Path, systemWhatsAppSettingsPrefix), "/")
	if phoneNumberID == "" {
		respondEnvelope(w, http.StatusNotFound, "not found", nil)
		return
	}
	if r.Method != http.MethodDelete {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	h.disconnectWhatsAppAccountFor(w, r, identity.SystemTenantID, phoneNumberID)
}

func (h *Handler) systemWhatsAppTemplates(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformMessaging(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listWhatsAppTemplatesFor(w, r, identity.SystemTenantID)
	case http.MethodPost:
		h.createWhatsAppTemplateFor(w, r, identity.SystemTenantID)
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
