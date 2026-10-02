package application

import (
	"context"
	"log/slog"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

// PlatformMessagingNotifier emits inbox events for the platform number. Every
// method is best effort and safe on a nil receiver: the REST history is the
// source of truth, events only remove latency.
type PlatformMessagingNotifier struct {
	repository PlatformMessagingRepository
	publisher  PlatformMessagingPublisher
	clock      Clock
	log        *slog.Logger
}

func NewPlatformMessagingNotifier(repository PlatformMessagingRepository, publisher PlatformMessagingPublisher, clock Clock, log *slog.Logger) *PlatformMessagingNotifier {
	return &PlatformMessagingNotifier{repository: repository, publisher: publisher, clock: clock, log: log}
}

func (n *PlatformMessagingNotifier) Covers(tenantID string) bool {
	return n != nil && tenantID == identity.SystemTenantID
}

func (n *PlatformMessagingNotifier) MessageCreated(ctx context.Context, message domain.WhatsAppConversationMessage) {
	if !n.Covers(message.TenantID) {
		return
	}
	n.report("message.created", n.publisher.PublishMessageCreated(ctx, MessageCreatedView{
		ConversationID: message.ConversationID,
		Message:        NewMessageView(message),
	}))
}

func (n *PlatformMessagingNotifier) MessageStatus(ctx context.Context, message domain.WhatsAppConversationMessage) {
	if !n.Covers(message.TenantID) {
		return
	}
	n.report("message.status", n.publisher.PublishMessageStatus(ctx, NewMessageStatusView(message)))
}

func (n *PlatformMessagingNotifier) MessageDeleted(ctx context.Context, message domain.WhatsAppConversationMessage) {
	if !n.Covers(message.TenantID) {
		return
	}
	view := MessageDeletedView{ConversationID: message.ConversationID, MessageID: message.ID, ClientMessageID: optionalText(message.ClientMessageID)}
	n.report("message.deleted", n.publisher.PublishMessageDeleted(ctx, view))
}

func (n *PlatformMessagingNotifier) Failure(ctx context.Context, conversationID, clientMessageID, code string) {
	if n == nil {
		return
	}
	view := MessageStatusView{ConversationID: conversationID, Status: "failed", Error: newErrorView(code)}
	if clientMessageID != "" {
		view.ClientMessageID = &clientMessageID
	}
	n.report("message.status", n.publisher.PublishMessageStatus(ctx, view))
}

func (n *PlatformMessagingNotifier) ConversationUpdated(ctx context.Context, conversation domain.WhatsAppConversation) {
	if !n.Covers(conversation.TenantID) {
		return
	}
	unread, err := n.repository.ConversationUnreadCount(ctx, conversation.TenantID, conversation.ID)
	if err != nil {
		n.report("conversation.updated", err)
		return
	}
	conversation.UnreadCount = unread
	n.report("conversation.updated", n.publisher.PublishConversationUpdated(ctx, ConversationUpdatedView{
		ConversationID: conversation.ID,
		Conversation:   NewConversationView(conversation, n.clock.Now()),
	}))
}

func (n *PlatformMessagingNotifier) report(event string, err error) {
	if err != nil && n.log != nil {
		n.log.Warn("platform_messaging_emit_failed", "event", event, "error", err.Error())
	}
}
