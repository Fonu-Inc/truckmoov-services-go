package application

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type OutboxRepository interface {
	Save(message domain.OutboxMessage) error
	FindByID(id string) (domain.OutboxMessage, bool)
	FindByProviderMessageID(providerMessageID string) (domain.OutboxMessage, bool)
}

type MessageProvider interface {
	Send(ctx context.Context, channel, recipient, content string) (status string, err error)
}

type WhatsAppTemplateMessageProvider interface {
	SendTemplate(ctx context.Context, request domain.WhatsAppTemplateMessageRequest) (domain.WhatsAppSendResult, error)
}

type WhatsAppConversationMessageProvider interface {
	SendText(ctx context.Context, recipient, content string) (domain.WhatsAppSendResult, error)
	SendMedia(ctx context.Context, request domain.WhatsAppMediaMessageRequest) (domain.WhatsAppSendResult, error)
}

type WhatsAppTemplateProvider interface {
	CreateTemplate(ctx context.Context, request domain.WhatsAppTemplateCreateRequest) (domain.WhatsAppTemplateCreateResponse, error)
	ListTemplates(ctx context.Context, request domain.WhatsAppTemplateListRequest) (domain.WhatsAppTemplateListResponse, error)
}

type WhatsAppConversationRepository interface {
	EnsureSchema(ctx context.Context) error
	FindConversationByTenantAndID(ctx context.Context, tenantID, id string) (domain.WhatsAppConversation, bool, error)
	FindConversationByTenantAndDriverPhone(ctx context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error)
	FindMostRecentConversationByDriverPhone(ctx context.Context, driverPhone string) (domain.WhatsAppConversation, bool, error)
	ListConversationsByTenant(ctx context.Context, tenantID string) ([]domain.WhatsAppConversation, error)
	SaveConversation(ctx context.Context, conversation domain.WhatsAppConversation) (domain.WhatsAppConversation, error)
	SaveMessage(ctx context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error)
	ListMessagesByConversation(ctx context.Context, tenantID, conversationID string) ([]domain.WhatsAppConversationMessage, error)
	FindMessageByProviderMessageID(ctx context.Context, providerMessageID string) (domain.WhatsAppConversationMessage, bool, error)
}

type DeliveryEventPublisher interface {
	PublishDeliveryUpdated(ctx context.Context, event domain.MessageDeliveryUpdated) error
}

type CreateDriverStatusRequestFromWhatsAppCommand struct {
	TenantID        string
	JobID           string
	DriverID        string
	DriverPhone     string
	Keyword         string
	RawMessage      string
	SourceMessageID string
	Channel         string
}

type DriverStatusRequestReceipt struct {
	RequestID       string
	RequestedStatus string
}

type DriverStatusRequestSender interface {
	CreateDriverStatusRequest(ctx context.Context, command CreateDriverStatusRequestFromWhatsAppCommand) (DriverStatusRequestReceipt, error)
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	New(prefix string) string
}
