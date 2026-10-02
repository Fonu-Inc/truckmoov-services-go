package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/identity"
)

const clientID = "3f0c6c1e-8f0a-4a63-9d53-0c3b3f6f2a11"

func sendCommand(conversationID string) PlatformSendCommand {
	return PlatformSendCommand{ClientMessageID: clientID, ConversationID: conversationID, Text: " hello ", ActorID: "staff-1"}
}

func TestPlatformSendStoresQueuedRowThenReportsSent(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)

	if err := kit.send.Execute(context.Background(), sendCommand("wac-1")); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := kit.publisher.names(); got != "message.created,conversation.updated,message.status,conversation.updated" {
		t.Fatalf("unexpected events: %s", got)
	}
	created := kit.publisher.events[0].Created
	if created.Message.Status != "queued" || *created.Message.ClientMessageID != clientID || *created.Message.Text != "hello" {
		t.Fatalf("unexpected created message: %+v", created.Message)
	}
	status := kit.publisher.last("message.status").Status
	if status.Status != "sent" || *status.ClientMessageID != clientID || status.MessageID != created.Message.ID {
		t.Fatalf("unexpected status: %+v", status)
	}
	stored, _, _ := kit.repo.FindMessageByClientID(context.Background(), identity.SystemTenantID, clientID)
	if stored.ProviderMessageID != "wamid.out1" || stored.Status != "sent" || stored.SenderSubject != "staff-1" {
		t.Fatalf("unexpected stored row: %+v", stored)
	}
	conversation := kit.publisher.last("conversation.updated").Updated.Conversation
	if conversation.LastMessagePreview != "hello" || conversation.LastMessageDirection != "outbound" {
		t.Fatalf("conversation summary not updated: %+v", conversation)
	}
}

func TestPlatformSendIsIdempotentOnClientMessageID(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)

	for range 3 {
		if err := kit.send.Execute(context.Background(), sendCommand("wac-1")); err != nil {
			t.Fatalf("execute: %v", err)
		}
	}

	if kit.provider.calls != 1 {
		t.Fatalf("provider called %d times, want 1", kit.provider.calls)
	}
	if rows := kit.repo.messagesOf(identity.SystemTenantID, "wac-1"); len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	replays := 0
	for _, event := range kit.publisher.events[4:] {
		if event.Name == "message.created" {
			replays++
		}
	}
	if replays != 2 {
		t.Fatalf("want 2 replayed message.created events, got %d", replays)
	}
}

func TestPlatformSendOnClosedWindowFailsWithoutProvider(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", false)

	if err := kit.send.Execute(context.Background(), sendCommand("wac-1")); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if kit.provider.calls != 0 {
		t.Fatal("provider must not be called when the window is closed")
	}
	status := kit.publisher.last("message.status").Status
	if status.Status != "failed" || status.Error == nil || status.Error.Code != "window_locked" {
		t.Fatalf("unexpected status: %+v", status)
	}
	stored, ok, _ := kit.repo.FindMessageByClientID(context.Background(), identity.SystemTenantID, clientID)
	if !ok || stored.Status != "failed" {
		t.Fatalf("failed row not stored: %+v", stored)
	}
}

func TestPlatformSendProviderFailureMarksRowFailed(t *testing.T) {
	kit := newInboxKit()
	kit.provider.err = errors.New("meta down")
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)

	if err := kit.send.Execute(context.Background(), sendCommand("wac-1")); err != nil {
		t.Fatalf("execute: %v", err)
	}

	status := kit.publisher.last("message.status").Status
	if status.Status != "failed" || status.Error.Code != "send_failed" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Error.Message == "meta down" {
		t.Fatal("provider error text must not reach the client")
	}
}

func TestPlatformSendUnknownAndCrossTenantConversationsAreNotFound(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, "tenant-a", "wac-tenant", true)

	for _, id := range []string{"missing", "wac-tenant"} {
		kit.publisher.events = nil
		if err := kit.send.Execute(context.Background(), sendCommand(id)); err != nil {
			t.Fatalf("execute: %v", err)
		}
		status := kit.publisher.last("message.status").Status
		if status == nil || status.Error == nil || status.Error.Code != "conversation_not_found" {
			t.Fatalf("%s: unexpected status: %+v", id, status)
		}
	}
	if kit.provider.calls != 0 {
		t.Fatal("provider must not be called")
	}
}

type staticStaff map[string]bool

func (s staticStaff) CanManageMessaging(_ context.Context, userID string) bool { return s[userID] }

func commandsFor(kit *inboxKit, staff staticStaff) *PlatformMessagingCommands {
	clock := fixedNotificationClock{now: inboxNow}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	read := NewPlatformMessagingReadUseCase(kit.repo, kit.notifier, clock)
	return NewPlatformMessagingCommands(kit.send, read, staff, kit.repo, kit.notifier, log)
}

func sendEnvelope(actor string) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{
		ID: clientID, TenantID: identity.SystemTenantID, Actor: map[string]any{"user_id": actor},
		Data: map[string]any{"client_message_id": clientID, "conversation_id": "wac-1", "text": "hello"},
	}
}

func TestCommandSendRequiresAStaffUserWhoMayReply(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)
	commands := commandsFor(kit, staticStaff{"staff-1": true})

	if err := commands.HandleSend(context.Background(), sendEnvelope("intruder")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if kit.provider.calls != 0 || kit.publisher.names() != "message.status" {
		t.Fatalf("a refused actor must cause no send and one forbidden status, got %s", kit.publisher.names())
	}
	if status := kit.publisher.last("message.status").Status; status == nil || status.Error == nil || status.Error.Code != "forbidden" || status.ConversationID != "wac-1" {
		t.Fatalf("want forbidden status for the conversation, got %+v", status)
	}

	if err := commands.HandleSend(context.Background(), sendEnvelope("staff-1")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if kit.provider.calls != 1 {
		t.Fatalf("authorised actor should send once, got %d", kit.provider.calls)
	}
}

func TestCommandSendForbiddenOnExistingRowReportsForbidden(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)
	if err := commandsFor(kit, staticStaff{"staff-1": true}).HandleSend(context.Background(), sendEnvelope("staff-1")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	kit.publisher.events = nil

	if err := commandsFor(kit, staticStaff{}).HandleSend(context.Background(), sendEnvelope("staff-1")); err != nil {
		t.Fatalf("handle: %v", err)
	}

	status := kit.publisher.last("message.status").Status
	if status == nil || status.Error.Code != "forbidden" {
		t.Fatalf("want forbidden status, got %+v", status)
	}
	if kit.provider.calls != 1 {
		t.Fatalf("provider calls = %d", kit.provider.calls)
	}
}

func TestCommandSendDropsMalformedAndWrongTenantEnvelopes(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)
	commands := commandsFor(kit, staticStaff{"staff-1": true})

	wrongTenant := sendEnvelope("staff-1")
	wrongTenant.TenantID = "tenant-a"
	noActor := sendEnvelope("")
	blank := sendEnvelope("staff-1")
	blank.Data["text"] = "   "
	huge := sendEnvelope("staff-1")
	huge.Data["text"] = strings.Repeat("x", 4097)
	for _, envelope := range []eventbus.EventEnvelope{wrongTenant, noActor, blank, huge} {
		if err := commands.HandleSend(context.Background(), envelope); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
	if kit.provider.calls != 0 {
		t.Fatalf("dropped envelopes must not send, calls=%d", kit.provider.calls)
	}
}

func TestCommandReadClearsUnreadAndAnnounces(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)
	if _, err := kit.repo.SaveMessage(context.Background(), domain.WhatsAppConversationMessage{
		ID: "wam-in", TenantID: identity.SystemTenantID, ConversationID: "wac-1", Direction: domain.WhatsAppDirectionInbound,
		MessageType: "text", Status: "received", Text: "hi", CreatedAt: inboxNow.Add(-time.Minute), UpdatedAt: inboxNow,
	}); err != nil {
		t.Fatal(err)
	}
	commands := commandsFor(kit, staticStaff{"staff-1": true})
	envelope := eventbus.EventEnvelope{TenantID: identity.SystemTenantID, Actor: map[string]any{"user_id": "staff-1"}, Data: map[string]any{"conversation_id": "wac-1"}}

	if unread, _ := kit.repo.ConversationUnreadCount(context.Background(), identity.SystemTenantID, "wac-1"); unread != 1 {
		t.Fatalf("setup: unread = %d", unread)
	}
	if err := commands.HandleRead(context.Background(), envelope); err != nil {
		t.Fatalf("handle: %v", err)
	}

	updated := kit.publisher.last("conversation.updated").Updated
	if updated == nil || updated.Conversation.UnreadCount != 0 {
		t.Fatalf("want unread 0 announced, got %+v", updated)
	}

	envelope.Actor = map[string]any{"user_id": "intruder"}
	kit.publisher.events = nil
	if err := commands.HandleRead(context.Background(), envelope); err != nil || len(kit.publisher.events) != 0 {
		t.Fatalf("intruder read must be ignored, events=%s err=%v", kit.publisher.names(), err)
	}
}

type fixedAccess struct {
	access identity.Access
	lookup identity.AccessLookup
}

func (f fixedAccess) Get(context.Context, string) (identity.Access, identity.AccessLookup) {
	return f.access, f.lookup
}

func TestAccessAuthorizer(t *testing.T) {
	manager := identity.Access{Active: true, Portal: identity.PortalAdmin, TenantID: identity.SystemTenantID, Permissions: []string{"admin.messaging.read", "admin.messaging.manage"}}
	cases := map[string]struct {
		access identity.Access
		lookup identity.AccessLookup
		want   bool
	}{
		"manager":          {manager, identity.AccessFound, true},
		"unknown user":     {manager, identity.AccessNoUser, false},
		"auth unreachable": {manager, identity.AccessUnknown, false},
		"suspended":        {withAccess(manager, func(a *identity.Access) { a.Active = false }), identity.AccessFound, false},
		"read only":        {withAccess(manager, func(a *identity.Access) { a.Permissions = []string{"admin.messaging.read"} }), identity.AccessFound, false},
		"tenant user":      {withAccess(manager, func(a *identity.Access) { a.Portal = identity.PortalTenant; a.TenantID = "tenant-a" }), identity.AccessFound, false},
	}
	for name, tc := range cases {
		if got := NewAccessAuthorizer(fixedAccess{tc.access, tc.lookup}).CanManageMessaging(context.Background(), "u"); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}

func withAccess(access identity.Access, change func(*identity.Access)) identity.Access {
	change(&access)
	return access
}

const tsSignedSend = `{"id":"3f0c6c1e-8f0a-4a63-9d53-0c3b3f6f2a11","type":"messaging.send.requested","version":1,"occurred_at":"2026-10-01T00:00:00.000Z","tenant_id":"system","actor":{"user_id":"staff-1"},"data":{"client_message_id":"3f0c6c1e-8f0a-4a63-9d53-0c3b3f6f2a11","conversation_id":"wac-1","text":"héllo \"q\""},"bsig":"5667d881bc5ce21014f922314438c4d638d831c63fb282ae2ae1b9b735935b78"}`

func wireEnvelope(t *testing.T, raw, subject string) eventbus.EventEnvelope {
	t.Helper()
	var envelope eventbus.EventEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Subject = subject
	return envelope
}

// The realtime gateway signs in TypeScript; this is that exact envelope.
func TestGatewaySignedSendCommandIsAcceptedAndForgeriesAreNot(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func(raw, subject, secret string) *inboxKit {
		kit := newInboxKit()
		kit.conversation(t, identity.SystemTenantID, "wac-1", true)
		handler := eventbus.Verified(commandsFor(kit, staticStaff{"staff-1": true}).HandleSend, secret, eventbus.VerifyOptions{}, log)
		if err := handler(context.Background(), wireEnvelope(t, raw, subject)); err != nil {
			t.Fatalf("handler: %v", err)
		}
		return kit
	}

	if kit := run(tsSignedSend, PlatformSendRequestedSubject, "xsecret"); kit.provider.calls != 1 {
		t.Fatalf("gateway-signed command should send once, calls=%d", kit.provider.calls)
	}
	unsigned := strings.Replace(tsSignedSend, `,"bsig":"5667d881bc5ce21014f922314438c4d638d831c63fb282ae2ae1b9b735935b78"`, "", 1)
	tampered := strings.Replace(tsSignedSend, "wac-1", "wac-2", 1)
	for name, kit := range map[string]*inboxKit{
		"unsigned":      run(unsigned, PlatformSendRequestedSubject, "xsecret"),
		"tampered":      run(tampered, PlatformSendRequestedSubject, "xsecret"),
		"wrong secret":  run(tsSignedSend, PlatformSendRequestedSubject, "other"),
		"wrong subject": run(tsSignedSend, "tm.system.messaging.read.requested", "xsecret"),
	} {
		if kit.provider.calls != 0 {
			t.Errorf("%s command must not send", name)
		}
	}
}

func TestCommandSendWithNulByteIsDroppedAndReportedFailed(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)
	envelope := sendEnvelope("staff-1")
	envelope.Data["text"] = "a\u0000b"

	if err := commandsFor(kit, staticStaff{"staff-1": true}).HandleSend(context.Background(), envelope); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if kit.provider.calls != 0 {
		t.Fatalf("control-character text reached the provider")
	}
	if _, found, _ := kit.repo.FindMessageByClientID(context.Background(), identity.SystemTenantID, clientID); found {
		t.Fatalf("control-character text was stored")
	}
	status := kit.publisher.last("message.status").Status
	if status == nil || status.Error.Code != "send_failed" {
		t.Fatalf("want send_failed status, got %+v", status)
	}
}

func TestParsePlatformSendKeepsNewlinesAndTabs(t *testing.T) {
	envelope := sendEnvelope("staff-1")
	envelope.Data["text"] = "line one\r\n\tline two"
	if _, ok := parsePlatformSend(envelope); !ok {
		t.Fatal("newline and tab must stay valid")
	}
	envelope.Data["text"] = "bell\u0007"
	if _, ok := parsePlatformSend(envelope); ok {
		t.Fatal("bell control character must be rejected")
	}
}
