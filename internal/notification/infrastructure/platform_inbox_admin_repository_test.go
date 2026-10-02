package infrastructure

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

func adminRepositories(t *testing.T) map[string]application.PlatformInboxAdminRepository {
	t.Helper()
	repositories := map[string]application.PlatformInboxAdminRepository{}
	for name, repository := range inboxRepositories(t) {
		repositories[name] = repository.(application.PlatformInboxAdminRepository)
	}
	return repositories
}

func ptr[T any](value T) *T { return &value }

func TestPatchConversationChangesOnlyWhatIsGivenAndFilters(t *testing.T) {
	for name, repository := range adminRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			for index := range 3 {
				seedInboxConversation(t, repository, index)
			}
			tags := []string{"billing", "vip"}
			patched, ok, err := repository.PatchConversation(ctx, inboxTenant, "wac-1", domain.ConversationPatch{
				Status: ptr("pending"), AssigneeUserID: ptr("u-1"), Tags: &tags, Name: ptr("Ada L"),
			}, inboxEpoch)
			if err != nil || !ok || patched.Status != "pending" || patched.AssigneeUserID != "u-1" || patched.DriverName != "Ada L" || fmt.Sprint(patched.Tags) != "[billing vip]" {
				t.Fatalf("patched: %+v ok=%v err=%v", patched, ok, err)
			}
			if again, _, _ := repository.PatchConversation(ctx, inboxTenant, "wac-1", domain.ConversationPatch{Priority: ptr("urgent")}, inboxEpoch); again.Status != "pending" || again.Priority != "urgent" {
				t.Fatalf("an unset field must be kept: %+v", again)
			}
			if _, ok, _ := repository.PatchConversation(ctx, "tenant-a", "wac-1", domain.ConversationPatch{Status: ptr("resolved")}, inboxEpoch); ok {
				t.Fatal("another tenant must not change a platform conversation")
			}

			cases := map[string]domain.InboxFilter{
				"status":    {Status: "pending"},
				"assignee":  {Assignee: "u-1"},
				"tag":       {Tag: "vip"},
				"all three": {Status: "pending", Assignee: "u-1", Tag: "billing"},
			}
			for label, filter := range cases {
				got, err := repository.ListConversationsFiltered(ctx, inboxTenant, filter, nil, 10)
				if err != nil || fmt.Sprint(ids(got)) != "[wac-1]" {
					t.Errorf("%s: %v err=%v", label, ids(got), err)
				}
			}
			unassigned, _ := repository.ListConversationsFiltered(ctx, inboxTenant, domain.InboxFilter{Assignee: domain.AssigneeUnassigned}, nil, 10)
			if fmt.Sprint(ids(unassigned)) != "[wac-2 wac-0]" {
				t.Errorf("unassigned: %v", ids(unassigned))
			}
		})
	}
}

func TestMarkUnreadCountsAsOneUntilRead(t *testing.T) {
	for name, repository := range adminRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedInboxConversation(t, repository, 1)
			if _, _, err := repository.PatchConversation(ctx, inboxTenant, "wac-1", domain.ConversationPatch{MarkedUnread: ptr(true)}, inboxEpoch); err != nil {
				t.Fatal(err)
			}
			if count, _ := repository.ConversationUnreadCount(ctx, inboxTenant, "wac-1"); count != 1 {
				t.Fatalf("marked unread = %d", count)
			}
			unread, _ := repository.ListConversationsFiltered(ctx, inboxTenant, domain.InboxFilter{State: "unread"}, nil, 10)
			if len(unread) != 1 {
				t.Fatalf("unread list: %v", ids(unread))
			}
			if err := repository.MarkConversationRead(ctx, inboxTenant, "wac-1", inboxEpoch.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if count, _ := repository.ConversationUnreadCount(ctx, inboxTenant, "wac-1"); count != 0 {
				t.Fatalf("after read = %d", count)
			}
		})
	}
}

func TestResolvedConversationReopensWhenTheCustomerWritesAgain(t *testing.T) {
	for name, repository := range adminRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			conversation := seedInboxConversation(t, repository, 1)
			if _, _, err := repository.PatchConversation(ctx, inboxTenant, conversation.ID, domain.ConversationPatch{Status: ptr("resolved")}, inboxEpoch); err != nil {
				t.Fatal(err)
			}
			outbound := conversation
			outbound.LastOutboundMessageAt = &inboxEpoch
			if saved, _ := repository.SaveConversation(ctx, outbound); saved.Status != "resolved" {
				t.Fatalf("our own reply must not reopen: %s", saved.Status)
			}
			inbound := conversation
			later := inboxEpoch.Add(5 * time.Hour)
			inbound.LastInboundMessageAt = &later
			if saved, _ := repository.SaveConversation(ctx, inbound); saved.Status != "open" {
				t.Fatalf("a new customer message must reopen: %s", saved.Status)
			}
		})
	}
}

func TestStaleSaveDoesNotRollBackALaterInbound(t *testing.T) {
	for name, repository := range adminRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			stale := seedInboxConversation(t, repository, 1)
			newer := inboxEpoch.Add(10 * time.Hour)
			expires := newer.Add(24 * time.Hour)
			inbound := stale
			inbound.LastInboundMessageAt, inbound.WindowExpiresAt, inbound.LastMessageAt = &newer, &expires, &newer
			inbound.LastMessagePreview, inbound.LastMessageDirection = "reply", "inbound"
			if _, err := repository.SaveConversation(ctx, inbound); err != nil {
				t.Fatal(err)
			}

			older := inboxEpoch.Add(time.Hour)
			stale.LastOutboundMessageAt, stale.LastMessageAt = &older, &older
			stale.LastMessagePreview, stale.LastMessageDirection = "ours", "outbound"
			saved, err := repository.SaveConversation(ctx, stale)
			if err != nil {
				t.Fatal(err)
			}
			if saved.LastMessagePreview != "reply" || saved.LastMessageDirection != "inbound" || !saved.LastMessageAt.Equal(newer) {
				t.Fatalf("summary rolled back: %+v", saved)
			}
			if saved.LastInboundMessageAt == nil || !saved.LastInboundMessageAt.Equal(newer) || saved.WindowExpiresAt == nil || !saved.WindowExpiresAt.Equal(expires) {
				t.Fatalf("window rolled back: %+v", saved)
			}
		})
	}
}

func TestUpdateMessageNeverMovesDeliveredBackToSent(t *testing.T) {
	for name, repository := range adminRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedInboxConversation(t, repository, 1)
			message := domain.WhatsAppConversationMessage{
				ID: "m-out", TenantID: inboxTenant, ConversationID: "wac-1", Direction: "outbound", MessageType: "text",
				Status: "delivered", CreatedAt: inboxEpoch, UpdatedAt: inboxEpoch,
			}
			if _, err := repository.SaveMessage(ctx, message); err != nil {
				t.Fatal(err)
			}
			message.Status, message.ProviderMessageID = "sent", "wamid.1"
			saved, err := repository.UpdateMessage(ctx, message)
			if err != nil || saved.Status != "delivered" || saved.ProviderMessageID != "wamid.1" {
				t.Fatalf("saved: %+v err=%v", saved, err)
			}
			message.Status = "failed"
			if failed, _ := repository.UpdateMessage(ctx, message); failed.Status != "failed" {
				t.Fatalf("an explicit failure must still be stored: %s", failed.Status)
			}
		})
	}
}

func TestScheduledMessagesAreClaimedOnceAndCancelledOnlyBeforeSending(t *testing.T) {
	for name, repository := range adminRepositories(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedInboxConversation(t, repository, 1)
			due := inboxEpoch.Add(time.Hour)
			for index, at := range []time.Time{due, due.Add(48 * time.Hour)} {
				if _, err := repository.SaveMessage(ctx, domain.WhatsAppConversationMessage{
					ID: fmt.Sprintf("s-%d", index), TenantID: inboxTenant, ConversationID: "wac-1", ClientMessageID: fmt.Sprintf("c-%d", index),
					Direction: "outbound", MessageType: "text", Status: "scheduled", CreatedAt: inboxEpoch, UpdatedAt: inboxEpoch,
					Payload: map[string]any{"scheduled_at": at.Format(time.RFC3339)},
				}); err != nil {
					t.Fatal(err)
				}
			}
			list, err := repository.ListDueScheduledMessages(ctx, inboxTenant, due.Add(time.Minute), 10)
			if err != nil || fmt.Sprint(messageIDs(list)) != "[s-0]" {
				t.Fatalf("due: %v err=%v", messageIDs(list), err)
			}
			claimed, ok, err := repository.ClaimScheduledMessage(ctx, inboxTenant, "s-0", due.Add(time.Minute))
			if err != nil || !ok || claimed.Status != "queued" || !claimed.CreatedAt.Equal(due.Add(time.Minute)) {
				t.Fatalf("claim: %+v ok=%v err=%v", claimed, ok, err)
			}
			if _, ok, _ := repository.ClaimScheduledMessage(ctx, inboxTenant, "s-0", due.Add(time.Minute)); ok {
				t.Fatal("a second claim must lose")
			}
			if _, ok, _ := repository.DeleteScheduledMessage(ctx, inboxTenant, "s-0"); ok {
				t.Fatal("a claimed message cannot be cancelled")
			}
			if deleted, ok, _ := repository.DeleteScheduledMessage(ctx, inboxTenant, "s-1"); !ok || deleted.ID != "s-1" {
				t.Fatal("a waiting message can be cancelled")
			}
			if _, found, _ := repository.FindMessageByClientID(ctx, inboxTenant, "c-1"); found {
				t.Fatal("the cancelled message must be gone")
			}
		})
	}
}
