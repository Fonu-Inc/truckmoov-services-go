package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

const inboxTenant = "system"

var inboxEpoch = time.Unix(1_800_000_000, 0).UTC()

func inboxRepositories(t *testing.T) map[string]application.PlatformMessagingRepository {
	t.Helper()
	repositories := map[string]application.PlatformMessagingRepository{"memory": NewInMemoryWhatsAppConversationRepository()}
	dsn := os.Getenv("NOTIFICATION_TEST_PG_DSN")
	if dsn == "" {
		return repositories
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, table := range []string{"whatsapp_conversation_messages", "whatsapp_conversations"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE"); err != nil {
			t.Fatalf("drop: %v", err)
		}
	}
	repository := NewPostgresWhatsAppConversationRepository(db)
	for range 2 {
		if err := repository.EnsureSchema(context.Background()); err != nil {
			t.Fatalf("ensure schema: %v", err)
		}
	}
	repositories["postgres"] = repository
	return repositories
}

func seedInboxConversation(t *testing.T, repository application.PlatformMessagingRepository, index int) domain.WhatsAppConversation {
	t.Helper()
	at := inboxEpoch.Add(time.Duration(index) * time.Hour)
	saved, err := repository.SaveConversation(context.Background(), domain.WhatsAppConversation{
		ID: fmt.Sprintf("wac-%d", index), TenantID: inboxTenant, DriverPhone: fmt.Sprintf("23480000000%02d", index),
		DriverName: fmt.Sprintf("Driver %d", index), LastMessageAt: &at, LastMessagePreview: fmt.Sprintf("preview %d", index),
		CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatalf("save conversation: %v", err)
	}
	return saved
}

func seedInboxMessage(t *testing.T, repository application.PlatformMessagingRepository, id, conversationID, direction string, at time.Time) {
	t.Helper()
	if _, err := repository.SaveMessage(context.Background(), domain.WhatsAppConversationMessage{
		ID: id, TenantID: inboxTenant, ConversationID: conversationID, Direction: direction, MessageType: "text",
		Status: "received", Text: id, CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
}

func TestPlatformInboxRepositoryPagesConversationsAndCountsUnread(t *testing.T) {
	for name, repository := range inboxRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			for index := range 5 {
				seedInboxConversation(t, repository, index)
			}
			if _, err := repository.SaveConversation(ctx, domain.WhatsAppConversation{ID: "foreign", TenantID: "tenant-a", DriverPhone: "1", CreatedAt: inboxEpoch, UpdatedAt: inboxEpoch}); err != nil {
				t.Fatal(err)
			}
			seedInboxMessage(t, repository, "in-1", "wac-1", domain.WhatsAppDirectionInbound, inboxEpoch.Add(time.Minute))
			seedInboxMessage(t, repository, "in-2", "wac-1", domain.WhatsAppDirectionInbound, inboxEpoch.Add(2*time.Minute))
			seedInboxMessage(t, repository, "out-1", "wac-1", domain.WhatsAppDirectionOutbound, inboxEpoch.Add(3*time.Minute))

			first, err := repository.ListConversationsByTenantPage(ctx, inboxTenant, "", "all", nil, 3)
			if err != nil || len(first) != 3 || first[0].ID != "wac-4" || first[2].ID != "wac-2" {
				t.Fatalf("first page: %v err=%v", ids(first), err)
			}
			cursor := &domain.ConversationCursor{SortAt: domain.ConversationSortAt(first[2]), ID: first[2].ID}
			rest, err := repository.ListConversationsByTenantPage(ctx, inboxTenant, "", "all", cursor, 3)
			if err != nil || fmt.Sprint(ids(rest)) != "[wac-1 wac-0]" {
				t.Fatalf("rest: %v err=%v", ids(rest), err)
			}
			unread, err := repository.ListConversationsByTenantPage(ctx, inboxTenant, "", "unread", nil, 10)
			if err != nil || len(unread) != 1 || unread[0].ID != "wac-1" || unread[0].UnreadCount != 2 {
				t.Fatalf("unread: %+v err=%v", unread, err)
			}
			found, err := repository.ListConversationsByTenantPage(ctx, inboxTenant, "DRIVER 3", "all", nil, 10)
			if err != nil || fmt.Sprint(ids(found)) != "[wac-3]" {
				t.Fatalf("search: %v err=%v", ids(found), err)
			}
			wildcard, _ := repository.ListConversationsByTenantPage(ctx, inboxTenant, "%", "all", nil, 10)
			if len(wildcard) != 0 {
				t.Fatalf("a percent sign must be literal, got %v", ids(wildcard))
			}

			if err := repository.MarkConversationRead(ctx, inboxTenant, "wac-1", inboxEpoch.Add(90*time.Second)); err != nil {
				t.Fatal(err)
			}
			if count, _ := repository.ConversationUnreadCount(ctx, inboxTenant, "wac-1"); count != 1 {
				t.Fatalf("unread after mark read = %d, want 1 (in-2 arrived later)", count)
			}
			if err := repository.MarkConversationRead(ctx, "tenant-a", "wac-1", inboxEpoch.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if count, _ := repository.ConversationUnreadCount(ctx, inboxTenant, "wac-1"); count != 1 {
				t.Fatal("another tenant must not mark the platform conversation read")
			}
		})
	}
}

func TestPlatformInboxRepositoryMessagesAndClientIDs(t *testing.T) {
	for name, repository := range inboxRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedInboxConversation(t, repository, 1)
			for index := range 4 {
				seedInboxMessage(t, repository, fmt.Sprintf("m-%d", index), "wac-1", domain.WhatsAppDirectionInbound, inboxEpoch.Add(time.Duration(index)*time.Minute))
			}

			page, err := repository.ListMessagesPage(ctx, inboxTenant, "wac-1", "", 3)
			if err != nil || fmt.Sprint(messageIDs(page)) != "[m-3 m-2 m-1]" {
				t.Fatalf("page: %v err=%v", messageIDs(page), err)
			}
			older, err := repository.ListMessagesPage(ctx, inboxTenant, "wac-1", "m-1", 3)
			if err != nil || fmt.Sprint(messageIDs(older)) != "[m-0]" {
				t.Fatalf("older: %v err=%v", messageIDs(older), err)
			}
			if empty, _ := repository.ListMessagesPage(ctx, inboxTenant, "wac-1", "missing", 3); len(empty) != 0 {
				t.Fatalf("unknown anchor must return nothing, got %v", messageIDs(empty))
			}
			if foreign, _ := repository.ListMessagesPage(ctx, "tenant-a", "wac-1", "", 3); len(foreign) != 0 {
				t.Fatal("another tenant must see no messages")
			}

			queued := domain.WhatsAppConversationMessage{
				ID: "out-1", TenantID: inboxTenant, ConversationID: "wac-1", ClientMessageID: "cid-1", Direction: domain.WhatsAppDirectionOutbound,
				MessageType: "text", Status: "queued", Text: "hi", Payload: map[string]any{"type": "text"}, CreatedAt: inboxEpoch, UpdatedAt: inboxEpoch,
			}
			if _, err := repository.SaveMessage(ctx, queued); err != nil {
				t.Fatalf("save queued: %v", err)
			}
			duplicate := queued
			duplicate.ID = "out-2"
			if _, err := repository.SaveMessage(ctx, duplicate); err == nil {
				t.Fatal("a second row with the same client message id must be refused")
			}
			found, ok, err := repository.FindMessageByClientID(ctx, inboxTenant, "cid-1")
			if err != nil || !ok || found.ID != "out-1" {
				t.Fatalf("find by client id: %+v ok=%v err=%v", found, ok, err)
			}
			if _, ok, _ := repository.FindMessageByClientID(ctx, "tenant-a", "cid-1"); ok {
				t.Fatal("client ids are scoped to the tenant")
			}

			queued.Status, queued.ProviderMessageID, queued.UpdatedAt = "sent", "wamid.1", inboxEpoch.Add(time.Second)
			queued.Payload = map[string]any{"type": "text", "note": "x"}
			updated, err := repository.UpdateMessage(ctx, queued)
			if err != nil || updated.Status != "sent" || updated.ProviderMessageID != "wamid.1" || updated.ClientMessageID != "cid-1" {
				t.Fatalf("update: %+v err=%v", updated, err)
			}
			byProvider, ok, _ := repository.FindMessageByProviderMessageID(ctx, "wamid.1")
			if !ok || byProvider.ID != "out-1" {
				t.Fatalf("provider lookup after update: %+v", byProvider)
			}
		})
	}
}

func ids(conversations []domain.WhatsAppConversation) []string {
	out := make([]string, 0, len(conversations))
	for _, conversation := range conversations {
		out = append(out, conversation.ID)
	}
	return out
}

func messageIDs(messages []domain.WhatsAppConversationMessage) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.ID)
	}
	return out
}
