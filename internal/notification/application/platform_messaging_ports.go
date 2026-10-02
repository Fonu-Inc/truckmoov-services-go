package application

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// PlatformMessagingRepository is what the staff inbox needs beyond the
// conversation store every tenant shares.
type PlatformMessagingRepository interface {
	WhatsAppConversationRepository
	ListConversationsByTenantPage(ctx context.Context, tenantID, query, state string, cursor *domain.ConversationCursor, limit int) ([]domain.WhatsAppConversation, error)
	ListConversationsFiltered(ctx context.Context, tenantID string, filter domain.InboxFilter, cursor *domain.ConversationCursor, limit int) ([]domain.WhatsAppConversation, error)
	ListMessagesPage(ctx context.Context, tenantID, conversationID, before string, limit int) ([]domain.WhatsAppConversationMessage, error)
	MarkConversationRead(ctx context.Context, tenantID, conversationID string, at time.Time) error
	FindMessageByClientID(ctx context.Context, tenantID, clientMessageID string) (domain.WhatsAppConversationMessage, bool, error)
	UpdateMessage(ctx context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error)
	ConversationUnreadCount(ctx context.Context, tenantID, conversationID string) (int, error)
}

// PlatformMessagingPublisher emits inbox events to the realtime gateway.
type PlatformMessagingPublisher interface {
	PublishMessageCreated(ctx context.Context, payload MessageCreatedView) error
	PublishMessageStatus(ctx context.Context, payload MessageStatusView) error
	PublishConversationUpdated(ctx context.Context, payload ConversationUpdatedView) error
	PublishMessageDeleted(ctx context.Context, payload MessageDeletedView) error
}

// PlatformStaffAuthorizer says whether a user may act in the platform inbox.
type PlatformStaffAuthorizer interface {
	CanManageMessaging(ctx context.Context, userID string) bool
}

// PlatformInboxAdminRepository is what the staff-owned conversation fields,
// notes, scheduled sends and media lookups need.
type PlatformInboxAdminRepository interface {
	PlatformMessagingRepository
	PatchConversation(ctx context.Context, tenantID, id string, patch domain.ConversationPatch, at time.Time) (domain.WhatsAppConversation, bool, error)
	FindMessageByID(ctx context.Context, tenantID, id string) (domain.WhatsAppConversationMessage, bool, error)
	DeleteScheduledMessage(ctx context.Context, tenantID, id string) (domain.WhatsAppConversationMessage, bool, error)
	ListDueScheduledMessages(ctx context.Context, tenantID string, now time.Time, limit int) ([]domain.WhatsAppConversationMessage, error)
	ClaimScheduledMessage(ctx context.Context, tenantID, id string, now time.Time) (domain.WhatsAppConversationMessage, bool, error)
}

type CannedReplyRepository interface {
	ListCannedReplies(ctx context.Context) ([]domain.CannedReply, error)
	SaveCannedReply(ctx context.Context, reply domain.CannedReply) (domain.CannedReply, error)
	DeleteCannedReply(ctx context.Context, id string) (bool, error)
}

// PlatformMediaProvider moves media between staff and the platform number.
type PlatformMediaProvider interface {
	UploadMedia(ctx context.Context, tenantID, filename, contentType string, data []byte) (string, error)
	DownloadMedia(ctx context.Context, tenantID, mediaID string) (data []byte, contentType string, err error)
	SendMedia(ctx context.Context, request domain.WhatsAppMediaMessageRequest) (domain.WhatsAppSendResult, error)
}
