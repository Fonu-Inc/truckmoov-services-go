package application

import (
	"context"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type OutboxRepository interface {
	Save(message domain.OutboxMessage) error
	FindByTenantAndID(tenantID, id string) (domain.OutboxMessage, bool)
	FindByProviderMessageID(providerMessageID string) (domain.OutboxMessage, bool)
}

type MessageProvider interface {
	Send(ctx context.Context, tenantID, channel, recipient, content string) (status string, err error)
}

type WhatsAppTemplateMessageProvider interface {
	SendTemplate(ctx context.Context, request domain.WhatsAppTemplateMessageRequest) (domain.WhatsAppSendResult, error)
}

type WhatsAppConversationMessageProvider interface {
	SendText(ctx context.Context, tenantID, recipient, content string) (domain.WhatsAppSendResult, error)
	SendMedia(ctx context.Context, request domain.WhatsAppMediaMessageRequest) (domain.WhatsAppSendResult, error)
}

type WhatsAppTemplateProvider interface {
	CreateTemplate(ctx context.Context, request domain.WhatsAppTemplateCreateRequest) (domain.WhatsAppTemplateCreateResponse, error)
	ListTemplates(ctx context.Context, request domain.WhatsAppTemplateListRequest) (domain.WhatsAppTemplateListResponse, error)
}

type WhatsAppAccountRepository interface {
	EnsureSchema(ctx context.Context) error
	SaveAccount(ctx context.Context, account domain.WhatsAppAccount) (domain.WhatsAppAccount, error)
	FindAccountByTenantID(ctx context.Context, tenantID string) (domain.WhatsAppAccount, bool, error)
	FindAccountByPhoneNumberID(ctx context.Context, phoneNumberID string) (domain.WhatsAppAccount, bool, error)
	DeleteAccountByTenantAndPhoneNumberID(ctx context.Context, tenantID, phoneNumberID string) error
}

type WhatsAppEmbeddedSignupProvider interface {
	ExchangeCodeForLongLivedToken(ctx context.Context, code string) (string, error)
	GetPhoneNumberProfile(ctx context.Context, accessToken, phoneNumberID string) (domain.WhatsAppAccount, error)
	GetBusinessProfile(ctx context.Context, accessToken, businessAccountID string) (domain.WhatsAppAccount, error)
	SubscribeBusinessApp(ctx context.Context, accessToken, businessAccountID string) error
}

type WhatsAppTokenCipher interface {
	Encrypt(token string) (ciphertext string, iv string, err error)
	Decrypt(ciphertext, iv string) (string, error)
}

type WhatsAppConversationRepository interface {
	EnsureSchema(ctx context.Context) error
	FindConversationByTenantAndID(ctx context.Context, tenantID, id string) (domain.WhatsAppConversation, bool, error)
	FindConversationByTenantAndDriverPhone(ctx context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error)
	FindMostRecentConversationByDriverPhone(ctx context.Context, driverPhone string) (domain.WhatsAppConversation, bool, error)
	FindMostRecentConversationByTenantAndDriverPhone(ctx context.Context, tenantID, driverPhone string) (domain.WhatsAppConversation, bool, error)
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
