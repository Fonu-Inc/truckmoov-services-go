package application

import (
	"context"
	"errors"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

type PlatformSendCommand struct {
	ClientMessageID string
	ConversationID  string
	Text            string
	ActorID         string
}

// PlatformMessagingSendUseCase sends a staff reply from the platform number.
// Delivery itself is the tenant text-message path; this adds the inbox row,
// idempotency by client id and the live events around it.
type PlatformMessagingSendUseCase struct {
	platformOutbound
	text *SendWhatsAppTextMessageUseCase
}

func NewPlatformMessagingSendUseCase(repository PlatformMessagingRepository, text *SendWhatsAppTextMessageUseCase, notifier *PlatformMessagingNotifier, clock Clock, idGenerator IDGenerator) *PlatformMessagingSendUseCase {
	return &PlatformMessagingSendUseCase{
		platformOutbound: platformOutbound{repository: repository, notifier: notifier, clock: clock, idGenerator: idGenerator},
		text:             text,
	}
}

func (u *PlatformMessagingSendUseCase) Execute(ctx context.Context, command PlatformSendCommand) error {
	tenantID := identity.SystemTenantID
	existing, found, err := u.repository.FindMessageByClientID(ctx, tenantID, command.ClientMessageID)
	if err != nil {
		return err
	}
	if found {
		return u.replay(ctx, existing)
	}
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, tenantID, command.ConversationID)
	if err != nil {
		return err
	}
	if !ok {
		u.notifier.Failure(ctx, command.ConversationID, command.ClientMessageID, ErrorCodeConversationNotFound)
		return nil
	}
	if conversation.IsLocked(u.clock.Now()) {
		return u.recordLocked(ctx, conversation, command)
	}
	return u.sendOpen(ctx, conversation, command)
}

func (u *PlatformMessagingSendUseCase) newTextMessage(conversation domain.WhatsAppConversation, command PlatformSendCommand, status string) domain.WhatsAppConversationMessage {
	text := strings.TrimSpace(command.Text)
	message := u.newMessage(conversation, command.ClientMessageID, command.ActorID, status)
	message.MessageType = "text"
	message.Text = text
	message.Payload = map[string]any{"type": "text", "body": text}
	return message
}

func (u *PlatformMessagingSendUseCase) recordLocked(ctx context.Context, conversation domain.WhatsAppConversation, command PlatformSendCommand) error {
	message := u.newTextMessage(conversation, command, "failed")
	message.Payload["error"] = errorPayload(ErrorCodeWindowLocked, "")
	saved, duplicate, err := u.saveNew(ctx, message)
	if err != nil || duplicate {
		return err
	}
	u.notifier.MessageCreated(ctx, saved)
	u.notifier.MessageStatus(ctx, saved)
	u.notifier.ConversationUpdated(ctx, conversation)
	return nil
}

func (u *PlatformMessagingSendUseCase) sendOpen(ctx context.Context, conversation domain.WhatsAppConversation, command PlatformSendCommand) error {
	queued, duplicate, err := u.saveNew(ctx, u.newTextMessage(conversation, command, "queued"))
	if err != nil || duplicate {
		return err
	}
	u.notifier.MessageCreated(ctx, queued)
	u.notifier.ConversationUpdated(ctx, conversation)
	return u.deliverText(ctx, queued, conversation)
}

// deliverText sends a stored text row and finishes it as sent or failed.
func (u *PlatformMessagingSendUseCase) deliverText(ctx context.Context, message domain.WhatsAppConversationMessage, conversation domain.WhatsAppConversation) error {
	outbox, err := u.text.queueOutbox(conversation.TenantID, conversation.DriverPhone, message.Text)
	if err != nil {
		return u.markFailed(ctx, message, conversation, err)
	}
	delivery, err := u.text.deliver(ctx, conversation.TenantID, outbox, conversation.DriverPhone, message.Text)
	if err != nil || delivery.SendErr != nil {
		return u.markFailed(ctx, message, conversation, errors.Join(err, delivery.SendErr))
	}
	return u.markSent(ctx, message, outboundKind{MessageType: "text", Text: message.Text}, delivery.Result)
}
