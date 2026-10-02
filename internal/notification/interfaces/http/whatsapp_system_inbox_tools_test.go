package notificationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
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

type toolsIDs struct{ n int }

func (i *toolsIDs) New(prefix string) string {
	i.n++
	return prefix + "-" + string(rune('a'+i.n))
}

type silentPublisher struct{}

func (silentPublisher) PublishMessageCreated(context.Context, application.MessageCreatedView) error {
	return nil
}
func (silentPublisher) PublishMessageStatus(context.Context, application.MessageStatusView) error {
	return nil
}
func (silentPublisher) PublishConversationUpdated(context.Context, application.ConversationUpdatedView) error {
	return nil
}
func (silentPublisher) PublishMessageDeleted(context.Context, application.MessageDeletedView) error {
	return nil
}

func toolsServer(t *testing.T, withTools bool) http.Handler {
	t.Helper()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock := inboxClock{now}
	repository := infrastructure.NewInMemoryWhatsAppConversationRepository()
	expires := now.Add(time.Hour)
	if _, err := repository.SaveConversation(context.Background(), domain.WhatsAppConversation{
		ID: "wac-1", TenantID: identity.SystemTenantID, DriverPhone: "2348000000001", LastMessageAt: &now, WindowExpiresAt: &expires, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SaveMessage(context.Background(), domain.WhatsAppConversationMessage{
		ID: "m-1", TenantID: identity.SystemTenantID, ConversationID: "wac-1", Direction: "inbound", MessageType: "text", Status: "received", Text: "hi", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	ids := &toolsIDs{}
	notifier := application.NewPlatformMessagingNotifier(repository, silentPublisher{}, clock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := (&Handler{clock: clock}).WithPlatformInbox(application.NewPlatformInboxQueries(repository, clock))
	if withTools {
		handler = handler.WithPlatformInboxTools(&PlatformInboxTools{
			Admin:  application.NewPlatformInboxAdmin(repository, notifier, clock, ids),
			Canned: application.NewCannedReplies(infrastructure.NewInMemoryCannedReplyRepository(), clock, ids),
		})
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	return httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))
}

func toolsCall(server http.Handler, method, path, body, tenantID, role string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, makeNotificationAuthRequestWithRole(method, path, bytes.NewBufferString(body).Bytes(), tenantID, role))
	return recorder
}

func staffCall(server http.Handler, method, path, body string) *httptest.ResponseRecorder {
	return toolsCall(server, method, path, body, "system", "SUPER_ADMIN")
}

func TestStaffActionsRefuseEveryoneButPlatformStaff(t *testing.T) {
	server := toolsServer(t, true)
	calls := []struct{ method, path, body string }{
		{http.MethodPatch, "/notify/system/whatsapp/conversations/wac-1", `{"status":"resolved"}`},
		{http.MethodPost, "/notify/system/whatsapp/conversations/wac-1/notes", `{"text":"x"}`},
		{http.MethodPost, "/notify/system/whatsapp/conversations/bulk", `{"ids":["wac-1"],"action":"read"}`},
		{http.MethodGet, "/notify/system/whatsapp/conversations/unread-summary", ""},
		{http.MethodGet, "/notify/system/whatsapp/canned-replies", ""},
		{http.MethodPost, "/notify/system/whatsapp/canned-replies", `{"shortcut":"a","title":"a","body":"a"}`},
		{http.MethodGet, "/notify/system/whatsapp/media/m-1", ""},
	}
	for _, call := range calls {
		if got := toolsCall(server, call.method, call.path, call.body, "", "").Code; got != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: %d", call.method, call.path, got)
		}
		if got := toolsCall(server, call.method, call.path, call.body, "tenant-a", "FLEET_ADMIN").Code; got != http.StatusForbidden {
			t.Errorf("%s %s tenant admin: %d", call.method, call.path, got)
		}
		if got := toolsCall(server, call.method, call.path, call.body, "system", "GOVERNANCE_OFFICER").Code; got != http.StatusForbidden {
			t.Errorf("%s %s staff without messaging: %d", call.method, call.path, got)
		}
	}
}

func TestStaffActionsAnswerUnavailableUntilConfigured(t *testing.T) {
	server := toolsServer(t, false)
	if got := staffCall(server, http.MethodPatch, "/notify/system/whatsapp/conversations/wac-1", `{"status":"resolved"}`).Code; got != http.StatusServiceUnavailable {
		t.Errorf("patch: %d", got)
	}
	if got := staffCall(server, http.MethodGet, "/notify/system/whatsapp/canned-replies", "").Code; got != http.StatusServiceUnavailable {
		t.Errorf("canned: %d", got)
	}
}

func TestPatchConversationValidatesAndReturnsTheView(t *testing.T) {
	server := toolsServer(t, true)
	patched := staffCall(server, http.MethodPatch, "/notify/system/whatsapp/conversations/wac-1", `{"status":"pending","assignee_user_id":"user-1","tags":["VIP"],"name":"Ada"}`)
	var view application.ConversationView
	if err := json.Unmarshal(decodeNotificationEnvelope(t, patched).Data, &view); err != nil || patched.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", patched.Code, patched.Body.String())
	}
	if view.Status != "pending" || view.AssigneeUserID == nil || *view.AssigneeUserID != "user-1" || view.Tags[0] != "vip" || view.DriverName == nil || *view.DriverName != "Ada" {
		t.Fatalf("view: %+v", view)
	}
	if got := staffCall(server, http.MethodPatch, "/notify/system/whatsapp/conversations/wac-1", `{"status":"closed"}`).Code; got != http.StatusBadRequest {
		t.Errorf("bad status: %d", got)
	}
	if got := staffCall(server, http.MethodPatch, "/notify/system/whatsapp/conversations/wac-1", `{"unknown":1}`).Code; got != http.StatusBadRequest {
		t.Errorf("unknown field: %d", got)
	}
	if got := staffCall(server, http.MethodPatch, "/notify/system/whatsapp/conversations/nope", `{"status":"open"}`).Code; got != http.StatusNotFound {
		t.Errorf("missing: %d", got)
	}
	filtered := staffCall(server, http.MethodGet, "/notify/system/whatsapp/conversations?status=pending&assignee=me&tag=vip", "")
	if !bytes.Contains(filtered.Body.Bytes(), []byte(`"wac-1"`)) {
		t.Fatalf("combined filter: %d %s", filtered.Code, filtered.Body.String())
	}
	mine := staffCall(server, http.MethodGet, "/notify/system/whatsapp/conversations?assignee=me", "")
	if !bytes.Contains(mine.Body.Bytes(), []byte(`"wac-1"`)) {
		t.Errorf("assignee=me should resolve to the caller (user-1): %s", mine.Body.String())
	}
	other := staffCall(server, http.MethodGet, "/notify/system/whatsapp/conversations?status=resolved", "")
	if bytes.Contains(other.Body.Bytes(), []byte(`"wac-1"`)) {
		t.Errorf("status filter ignored: %s", other.Body.String())
	}
}

func TestNotesBulkAndUnreadSummary(t *testing.T) {
	server := toolsServer(t, true)
	if got := staffCall(server, http.MethodPost, "/notify/system/whatsapp/conversations/wac-1/notes", `{"text":"call them"}`); got.Code != http.StatusCreated || !bytes.Contains(got.Body.Bytes(), []byte(`"message_type":"note"`)) {
		t.Fatalf("note: %d %s", got.Code, got.Body.String())
	}
	if got := staffCall(server, http.MethodPost, "/notify/system/whatsapp/conversations/wac-1/notes", `{"text":" "}`).Code; got != http.StatusBadRequest {
		t.Errorf("blank note: %d", got)
	}
	summary := staffCall(server, http.MethodGet, "/notify/system/whatsapp/conversations/unread-summary", "")
	if !bytes.Contains(summary.Body.Bytes(), []byte(`"conversations":1`)) {
		t.Fatalf("summary: %s", summary.Body.String())
	}
	if got := staffCall(server, http.MethodPost, "/notify/system/whatsapp/conversations/bulk", `{"ids":["wac-1"],"action":"read"}`); got.Code != http.StatusOK || !bytes.Contains(got.Body.Bytes(), []byte(`"changed":1`)) {
		t.Fatalf("bulk: %d %s", got.Code, got.Body.String())
	}
	if after := staffCall(server, http.MethodGet, "/notify/system/whatsapp/conversations/unread-summary", ""); !bytes.Contains(after.Body.Bytes(), []byte(`"conversations":0`)) {
		t.Fatalf("after read: %s", after.Body.String())
	}
	if got := staffCall(server, http.MethodPost, "/notify/system/whatsapp/conversations/bulk", `{"ids":["wac-1"]}`).Code; got != http.StatusBadRequest {
		t.Errorf("bulk with nothing to do: %d", got)
	}
}

func TestCannedReplyRoutes(t *testing.T) {
	server := toolsServer(t, true)
	created := staffCall(server, http.MethodPost, "/notify/system/whatsapp/canned-replies", `{"shortcut":"/ETA","title":"ETA","body":"On the way"}`)
	var reply domain.CannedReply
	if err := json.Unmarshal(decodeNotificationEnvelope(t, created).Data, &reply); err != nil || created.Code != http.StatusCreated || reply.Shortcut != "eta" {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if got := staffCall(server, http.MethodPost, "/notify/system/whatsapp/canned-replies", `{"shortcut":"eta","title":"Dup","body":"x"}`).Code; got != http.StatusConflict {
		t.Errorf("duplicate: %d", got)
	}
	if got := staffCall(server, http.MethodPatch, "/notify/system/whatsapp/canned-replies/"+reply.ID, `{"shortcut":"eta","title":"ETA 2","body":"Soon"}`).Code; got != http.StatusOK {
		t.Errorf("update: %d", got)
	}
	if got := staffCall(server, http.MethodPatch, "/notify/system/whatsapp/canned-replies/nope", `{"shortcut":"z","title":"z","body":"z"}`).Code; got != http.StatusNotFound {
		t.Errorf("update unknown: %d", got)
	}
	list := staffCall(server, http.MethodGet, "/notify/system/whatsapp/canned-replies", "")
	if !bytes.Contains(list.Body.Bytes(), []byte(`"ETA 2"`)) {
		t.Errorf("list: %s", list.Body.String())
	}
	if got := staffCall(server, http.MethodDelete, "/notify/system/whatsapp/canned-replies/"+reply.ID, "").Code; got != http.StatusNoContent {
		t.Errorf("delete: %d", got)
	}
	if got := staffCall(server, http.MethodDelete, "/notify/system/whatsapp/canned-replies/"+reply.ID, "").Code; got != http.StatusNotFound {
		t.Errorf("delete twice: %d", got)
	}
}
