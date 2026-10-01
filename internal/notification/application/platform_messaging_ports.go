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
}

// PlatformStaffAuthorizer says whether a user may act in the platform inbox.
type PlatformStaffAuthorizer interface {
	CanManageMessaging(ctx context.Context, userID string) bool
}
