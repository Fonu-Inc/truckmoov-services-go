package application

import (
	"context"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

// PlatformRetryUseCase sends a failed staff message again. Text and media are
// retried in place and stay inside the 24-hour window rule; a template is
// sent as a new message because it is allowed at any time.
type PlatformRetryUseCase struct {
	repository PlatformInboxAdminRepository
	send       *PlatformMessagingSendUseCase
	media      *PlatformMediaSendUseCase
	template   *PlatformTemplateSendUseCase
	notifier   *PlatformMessagingNotifier
}

func NewPlatformRetryUseCase(repository PlatformInboxAdminRepository, send *PlatformMessagingSendUseCase, media *PlatformMediaSendUseCase, template *PlatformTemplateSendUseCase, notifier *PlatformMessagingNotifier) *PlatformRetryUseCase {
	return &PlatformRetryUseCase{repository: repository, send: send, media: media, template: template, notifier: notifier}
}

func (u *PlatformRetryUseCase) Execute(ctx context.Context, messageID, actorID string) (MessageView, error) {
	message, ok, err := u.repository.FindMessageByID(ctx, identity.SystemTenantID, strings.TrimSpace(messageID))
	if err != nil {
		return MessageView{}, err
	}
	if !ok {
		return MessageView{}, ErrWhatsAppConversationNotFound
	}
	if message.Direction != domain.WhatsAppDirectionOutbound || message.SenderRole != platformStaffRole || message.Status != "failed" {
		return MessageView{}, InboxInputError("only a failed message you sent can be retried")
	}
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, message.TenantID, message.ConversationID)
	if err != nil {
		return MessageView{}, err
	}
	if !ok {
		return MessageView{}, ErrWhatsAppConversationNotFound
	}
	if message.MessageType == "template" {
		return u.retryTemplate(ctx, message, actorID)
	}
	if conversation.IsLocked(u.send.clock.Now()) {
		return MessageView{}, ErrWhatsAppConversationLocked
	}
	return u.retryInPlace(ctx, message, conversation)
}

func (u *PlatformRetryUseCase) retryInPlace(ctx context.Context, message domain.WhatsAppConversationMessage, conversation domain.WhatsAppConversation) (MessageView, error) {
	message.Status = "queued"
	message.Payload = cloneAnyMap(message.Payload)
	delete(message.Payload, "error")
	message.UpdatedAt = u.send.clock.Now().UTC()
	queued, err := u.repository.UpdateMessage(ctx, message)
	if err != nil {
		return MessageView{}, err
	}
	u.notifier.MessageStatus(ctx, queued)
	if queued.MediaID != "" {
		err = u.resendMedia(ctx, queued, conversation)
	} else {
		err = u.send.deliverText(ctx, queued, conversation)
	}
	if err != nil {
		return MessageView{}, err
	}
	latest, _, err := u.repository.FindMessageByID(ctx, queued.TenantID, queued.ID)
	return NewMessageView(latest), err
}

func (u *PlatformRetryUseCase) resendMedia(ctx context.Context, message domain.WhatsAppConversationMessage, conversation domain.WhatsAppConversation) error {
	filename, _ := message.Payload["filename"].(string)
	result, sendErr := u.media.media.SendMedia(ctx, domain.WhatsAppMediaMessageRequest{
		TenantID:  conversation.TenantID,
		Recipient: conversation.DriverPhone,
		MediaType: message.MediaType,
		MediaID:   message.MediaID,
		Caption:   message.Caption,
		Filename:  filename,
	})
	if sendErr != nil {
		return u.media.markFailed(ctx, message, conversation, sendErr)
	}
	return u.media.markSent(ctx, message, outboundKind{MessageType: message.MediaType, Caption: message.Caption, Text: filename}, result)
}

func (u *PlatformRetryUseCase) retryTemplate(ctx context.Context, message domain.WhatsAppConversationMessage, actorID string) (MessageView, error) {
	variables := map[string]string{}
	if raw, ok := message.Payload["variables"].(map[string]any); ok {
		for key, value := range raw {
			variables[key] = stringValue(value)
		}
	}
	language, _ := message.Payload["language"].(string)
	result, err := u.template.Execute(ctx, PlatformTemplateSendCommand{
		ClientMessageID: u.send.idGenerator.New("cm"),
		ConversationID:  message.ConversationID,
		TemplateName:    message.TemplateName,
		Language:        language,
		Variables:       variables,
		ActorID:         actorID,
	})
	return result.Message, err
}
