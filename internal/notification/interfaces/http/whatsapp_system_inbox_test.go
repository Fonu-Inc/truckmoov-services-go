package notificationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

type inboxClock struct{ now time.Time }

func (c inboxClock) Now() time.Time { return c.now }

func inboxServer(t *testing.T) http.Handler {
	t.Helper()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	repository := infrastructure.NewInMemoryWhatsAppConversationRepository()
	expires := now.Add(time.Hour)
	for _, conversation := range []domain.WhatsAppConversation{
		{ID: "wac-1", TenantID: identity.SystemTenantID, DriverPhone: "2348000000001", DriverName: "Ada", LastMessageAt: &now, WindowExpiresAt: &expires, CreatedAt: now, UpdatedAt: now},
		{ID: "wac-tenant", TenantID: "tenant-a", DriverPhone: "2348000000002", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.SaveConversation(context.Background(), conversation); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.SaveMessage(context.Background(), domain.WhatsAppConversationMessage{
		ID: "m-1", TenantID: identity.SystemTenantID, ConversationID: "wac-1", Direction: "inbound", MessageType: "image", Status: "received", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	handler := (&Handler{clock: inboxClock{now}}).WithPlatformInbox(application.NewPlatformInboxQueries(repository, inboxClock{now}))
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	return httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))
}

func inboxCall(server http.Handler, method, path, tenantID, role string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, makeNotificationAuthRequestWithRole(method, path, nil, tenantID, role))
	return recorder
}

func TestSystemInboxRoutesRefuseEveryoneButPlatformStaff(t *testing.T) {
	server := inboxServer(t)
	paths := []string{
		"/notify/system/whatsapp/conversations",
		"/notify/system/whatsapp/conversations/wac-1",
		"/notify/system/whatsapp/conversations/wac-1/messages",
	}
	for _, path := range paths {
		if got := inboxCall(server, http.MethodGet, path, "", "").Code; got != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", path, got)
		}
		if got := inboxCall(server, http.MethodGet, path, "tenant-a", "FLEET_ADMIN").Code; got != http.StatusForbidden {
			t.Errorf("%s tenant admin: %d", path, got)
		}
		if got := inboxCall(server, http.MethodGet, path, "system", "GOVERNANCE_OFFICER").Code; got != http.StatusForbidden {
			t.Errorf("%s staff without messaging: %d", path, got)
		}
		if got := inboxCall(server, http.MethodGet, path, "system", "SUPER_ADMIN").Code; got != http.StatusOK {
			t.Errorf("%s super admin: %d", path, got)
		}
		if got := inboxCall(server, http.MethodPost, path, "system", "SUPER_ADMIN").Code; got != http.StatusForbidden && got != http.StatusMethodNotAllowed {
			t.Errorf("%s post: %d", path, got)
		}
	}
}

func TestSystemInboxReturnsPlainPagesAndHidesTenantConversations(t *testing.T) {
	server := inboxServer(t)

	list := inboxCall(server, http.MethodGet, "/notify/system/whatsapp/conversations", "system", "SUPER_ADMIN")
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0]["id"] != "wac-1" {
		t.Fatalf("list: %s err=%v", list.Body.String(), err)
	}
	if page.Items[0]["unread_count"] != float64(1) || page.Items[0]["is_locked"] != false || page.Items[0]["driver_name"] != "Ada" {
		t.Fatalf("unexpected conversation: %v", page.Items[0])
	}

	messages := inboxCall(server, http.MethodGet, "/notify/system/whatsapp/conversations/wac-1/messages", "system", "SUPER_ADMIN")
	var thread struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(messages.Body.Bytes(), &thread); err != nil || len(thread.Items) != 1 {
		t.Fatalf("messages: %s", messages.Body.String())
	}
	if thread.Items[0]["message_type"] != "other" || thread.Items[0]["text"] != nil {
		t.Fatalf("non-text message must be other with null text: %v", thread.Items[0])
	}

	for _, path := range []string{"/notify/system/whatsapp/conversations/wac-tenant", "/notify/system/whatsapp/conversations/wac-tenant/messages", "/notify/system/whatsapp/conversations/nope"} {
		if got := inboxCall(server, http.MethodGet, path, "system", "SUPER_ADMIN").Code; got != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, got)
		}
	}
	if got := inboxCall(server, http.MethodGet, "/notify/system/whatsapp/conversations?cursor=%25%25", "system", "SUPER_ADMIN").Code; got != http.StatusBadRequest {
		t.Errorf("bad cursor: %d", got)
	}
}
