package application

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

func webhookFor(kit *inboxKit, tenantID string) *HandleWhatsAppWebhookUseCase {
	accounts := &fixedWhatsAppAccountRepository{account: domain.WhatsAppAccount{TenantID: tenantID, PhoneNumberID: "pn-1"}}
	useCase := NewHandleWhatsAppWebhookUseCase(
		kit.repo, kit.outbox, accounts, nil, nil, &fakeDeliveryPublisher{}, nil,
		fixedNotificationClock{now: inboxNow}, kit.ids,
	)
	return useCase.WithPlatformMessaging(kit.notifier)
}

func statusWebhook(providerID, status string) []byte {
	return []byte(fmt.Sprintf(`{"entry":[{"changes":[{"value":{"metadata":{"phone_number_id":"pn-1"},"statuses":[{"id":%q,"status":%q,"timestamp":"1800000100","recipient_id":"2348000000009"}]}}]}]}`, providerID, status))
}

func TestInboundOnPlatformNumberOpensConversationAndStreams(t *testing.T) {
	kit := newInboxKit()
	useCase := webhookFor(kit, identity.SystemTenantID)

	processed, err := useCase.Execute(context.Background(), HandleWhatsAppWebhookCommand{Payload: textWebhookPayload("pn-1", "2348000000001", "hello platform")})
	if err != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}

	conversation, ok, _ := kit.repo.FindConversationByTenantAndDriverPhone(context.Background(), identity.SystemTenantID, "2348000000001")
	if !ok || conversation.DriverName != "Driver" {
		t.Fatalf("conversation not opened: %+v ok=%v", conversation, ok)
	}
	if got := kit.publisher.names(); got != "message.created,conversation.updated" {
		t.Fatalf("unexpected events: %s", got)
	}
	created := kit.publisher.last("message.created").Created
	if created.Message.Status != "received" || created.Message.Direction != "inbound" || *created.Message.Text != "hello platform" {
		t.Fatalf("unexpected message: %+v", created.Message)
	}
	if unread := kit.publisher.last("conversation.updated").Updated.Conversation.UnreadCount; unread != 1 {
		t.Fatalf("unread = %d, want 1", unread)
	}
}

func TestInboundOnTenantNumberStillDropsUnknownSenders(t *testing.T) {
	kit := newInboxKit()
	useCase := webhookFor(kit, "tenant-a")

	processed, err := useCase.Execute(context.Background(), HandleWhatsAppWebhookCommand{Payload: textWebhookPayload("pn-1", "2348000000001", "hello")})
	if err != nil || processed != 0 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
	if _, ok, _ := kit.repo.FindConversationByTenantAndDriverPhone(context.Background(), "tenant-a", "2348000000001"); ok {
		t.Fatal("tenant conversation must not be auto-created")
	}
	if len(kit.publisher.events) != 0 {
		t.Fatalf("tenant traffic must not reach the staff inbox: %s", kit.publisher.names())
	}
}

func TestReceiptsKeepReadAndNeverMoveBackwards(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)
	if err := kit.send.Execute(context.Background(), sendCommand("wac-1")); err != nil {
		t.Fatal(err)
	}
	kit.publisher.events = nil
	useCase := webhookFor(kit, identity.SystemTenantID)

	for _, status := range []string{"delivered", "read", "delivered", "sent"} {
		if _, err := useCase.Execute(context.Background(), HandleWhatsAppWebhookCommand{Payload: statusWebhook("wamid.out1", status)}); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
	}

	var seen []string
	for _, event := range kit.publisher.events {
		if event.Name == "message.status" {
			seen = append(seen, event.Status.Status)
		}
	}
	if fmt.Sprint(seen) != "[delivered read]" {
		t.Fatalf("status events = %v, want [delivered read]", seen)
	}
	stored, _, _ := kit.repo.FindMessageByClientID(context.Background(), identity.SystemTenantID, clientID)
	if stored.Status != "read" {
		t.Fatalf("stored status = %s", stored.Status)
	}
}

func TestReceiptFailureAfterSendMarksFailedUnlessAlreadyDelivered(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)
	if err := kit.send.Execute(context.Background(), sendCommand("wac-1")); err != nil {
		t.Fatal(err)
	}
	kit.publisher.events = nil

	if _, err := webhookFor(kit, identity.SystemTenantID).Execute(context.Background(), HandleWhatsAppWebhookCommand{Payload: statusWebhook("wamid.out1", "failed")}); err != nil {
		t.Fatal(err)
	}

	status := kit.publisher.last("message.status").Status
	if status == nil || status.Status != "failed" || status.Error == nil || status.Error.Code != "send_failed" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func seedThread(t *testing.T, kit *inboxKit, conversationID string, count int) {
	t.Helper()
	for index := range count {
		direction := domain.WhatsAppDirectionInbound
		if index%2 == 1 {
			direction = domain.WhatsAppDirectionOutbound
		}
		at := inboxNow.Add(time.Duration(index) * time.Minute)
		if _, err := kit.repo.SaveMessage(context.Background(), domain.WhatsAppConversationMessage{
			ID: fmt.Sprintf("m-%02d", index), TenantID: identity.SystemTenantID, ConversationID: conversationID,
			Direction: direction, MessageType: "text", Status: "sent", Text: fmt.Sprintf("t%d", index), CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInboxQueriesPageMessagesOldestFirstWithinPage(t *testing.T) {
	kit := newInboxKit()
	kit.conversation(t, identity.SystemTenantID, "wac-1", true)
	seedThread(t, kit, "wac-1", 5)
	queries := NewPlatformInboxQueries(kit.repo, fixedNotificationClock{now: inboxNow})

	first, err := queries.ListMessages(context.Background(), "wac-1", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].ID != "m-03" || first.Items[1].ID != "m-04" || first.NextBefore == nil || *first.NextBefore != "m-03" {
		t.Fatalf("unexpected first page: %+v next=%v", first.Items, first.NextBefore)
	}
	last, err := queries.ListMessages(context.Background(), "wac-1", "m-01", 5)
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != "m-00" || last.NextBefore != nil {
		t.Fatalf("unexpected last page: %+v err=%v", last, err)
	}
	if _, err := queries.ListMessages(context.Background(), "nope", "", 5); err != ErrWhatsAppConversationNotFound {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestInboxQueriesPageConversationsByKeysetAndFilterUnread(t *testing.T) {
	kit := newInboxKit()
	for index := range 5 {
		at := inboxNow.Add(time.Duration(index) * time.Hour)
		kit.repo.SaveConversation(context.Background(), domain.WhatsAppConversation{
			ID: fmt.Sprintf("wac-%d", index), TenantID: identity.SystemTenantID, DriverPhone: fmt.Sprintf("23480000000%02d", index),
			LastMessageAt: &at, CreatedAt: at, UpdatedAt: at,
		})
	}
	kit.repo.SaveConversation(context.Background(), domain.WhatsAppConversation{ID: "other", TenantID: "tenant-a", DriverPhone: "1", CreatedAt: inboxNow, UpdatedAt: inboxNow})
	kit.repo.SaveMessage(context.Background(), domain.WhatsAppConversationMessage{ID: "u1", TenantID: identity.SystemTenantID, ConversationID: "wac-1", Direction: "inbound", MessageType: "text", CreatedAt: inboxNow, UpdatedAt: inboxNow})
	queries := NewPlatformInboxQueries(kit.repo, fixedNotificationClock{now: inboxNow})

	page1, err := queries.ListConversations(context.Background(), ListInboxQuery{Limit: 2})
	if err != nil || len(page1.Items) != 2 || page1.Items[0].ID != "wac-4" || page1.NextCursor == nil {
		t.Fatalf("page1: %+v err=%v", page1, err)
	}
	page2, err := queries.ListConversations(context.Background(), ListInboxQuery{Limit: 2, Cursor: *page1.NextCursor})
	if err != nil || len(page2.Items) != 2 || page2.Items[0].ID != "wac-2" {
		t.Fatalf("page2: %+v err=%v", page2, err)
	}
	page3, _ := queries.ListConversations(context.Background(), ListInboxQuery{Limit: 2, Cursor: *page2.NextCursor})
	if len(page3.Items) != 1 || page3.Items[0].ID != "wac-0" || page3.NextCursor != nil {
		t.Fatalf("page3: %+v", page3)
	}
	unread, _ := queries.ListConversations(context.Background(), ListInboxQuery{State: "unread"})
	if len(unread.Items) != 1 || unread.Items[0].ID != "wac-1" || unread.Items[0].UnreadCount != 1 {
		t.Fatalf("unread: %+v", unread.Items)
	}
	found, _ := queries.ListConversations(context.Background(), ListInboxQuery{Query: "0000003"})
	if len(found.Items) != 1 || found.Items[0].ID != "wac-3" {
		t.Fatalf("search: %+v", found.Items)
	}
	if _, err := queries.ListConversations(context.Background(), ListInboxQuery{Cursor: "%%%"}); err != ErrInvalidInboxCursor {
		t.Fatalf("want invalid cursor, got %v", err)
	}
	if _, err := queries.GetConversation(context.Background(), "other"); err != ErrWhatsAppConversationNotFound {
		t.Fatalf("cross-tenant get must be not found, got %v", err)
	}
}
