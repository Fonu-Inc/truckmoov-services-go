package application

import (
	"context"
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
	repository  PlatformMessagingRepository
	text        *SendWhatsAppTextMessageUseCase
	notifier    *PlatformMessagingNotifier
	clock       Clock
	idGenerator IDGenerator
}

func NewPlatformMessagingSendUseCase(repository PlatformMessagingRepository, text *SendWhatsAppTextMessageUseCase, notifier *PlatformMessagingNotifier, clock Clock, idGenerator IDGenerator) *PlatformMessagingSendUseCase {
	return &PlatformMessagingSendUseCase{repository: repository, text: text, notifier: notifier, clock: clock, idGenerator: idGenerator}
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

func (u *PlatformMessagingSendUseCase) newMessage(conversation domain.WhatsAppConversation, command PlatformSendCommand, status string) domain.WhatsAppConversationMessage {
	now := u.clock.Now().UTC()
	return domain.WhatsAppConversationMessage{
		ID:              u.idGenerator.New("wam"),
		TenantID:        conversation.TenantID,
		ConversationID:  conversation.ID,
		ClientMessageID: command.ClientMessageID,
		Direction:       domain.WhatsAppDirectionOutbound,
		MessageType:     "text",
		Status:          status,
		SenderRole:      "platform_staff",
		SenderSubject:   command.ActorID,
		ToPhone:         conversation.DriverPhone,
		Text:            strings.TrimSpace(command.Text),
		Payload:         map[string]any{"type": "text", "body": strings.TrimSpace(command.Text)},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// saveNew stores the row, treating a concurrent insert of the same client id
// as a replay of the winner.
func (u *PlatformMessagingSendUseCase) saveNew(ctx context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, bool, error) {
	saved, err := u.repository.SaveMessage(ctx, message)
	if err == nil {
		return saved, false, nil
	}
	existing, found, findErr := u.repository.FindMessageByClientID(ctx, message.TenantID, message.ClientMessageID)
	if findErr != nil || !found {
		return domain.WhatsAppConversationMessage{}, false, err
	}
	return existing, true, u.replay(ctx, existing)
}

func (u *PlatformMessagingSendUseCase) replay(ctx context.Context, message domain.WhatsAppConversationMessage) error {
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, message.TenantID, message.ConversationID)
	if err != nil {
		return err
	}
	u.notifier.MessageCreated(ctx, message)
	if ok {
		u.notifier.ConversationUpdated(ctx, conversation)
	}
	return nil
}

func (u *PlatformMessagingSendUseCase) recordLocked(ctx context.Context, conversation domain.WhatsAppConversation, command PlatformSendCommand) error {
	message := u.newMessage(conversation, command, "failed")
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
	queued, duplicate, err := u.saveNew(ctx, u.newMessage(conversation, command, "queued"))
	if err != nil || duplicate {
		return err
	}
	u.notifier.MessageCreated(ctx, queued)
	u.notifier.ConversationUpdated(ctx, conversation)

	outbox, err := u.text.queueOutbox(conversation.TenantID, conversation.DriverPhone, command.Text)
	if err != nil {
		return u.markFailed(ctx, queued, conversation)
	}
	delivery, err := u.text.deliver(ctx, conversation.TenantID, outbox, conversation.DriverPhone, command.Text)
	if err != nil || delivery.SendErr != nil {
		return u.markFailed(ctx, queued, conversation)
	}
	return u.markSent(ctx, queued, conversation, delivery.Result)
}

func (u *PlatformMessagingSendUseCase) markFailed(ctx context.Context, message domain.WhatsAppConversationMessage, conversation domain.WhatsAppConversation) error {
	message.Status = "failed"
	message.Payload = mergeAnyMap(message.Payload, map[string]any{"error": errorPayload(ErrorCodeSendFailed, "")})
	message.UpdatedAt = u.clock.Now().UTC()
	saved, err := u.repository.UpdateMessage(ctx, message)
	if err != nil {
		return err
	}
	u.notifier.MessageStatus(ctx, saved)
	u.notifier.ConversationUpdated(ctx, conversation)
	return nil
}

func (u *PlatformMessagingSendUseCase) markSent(ctx context.Context, message domain.WhatsAppConversationMessage, conversation domain.WhatsAppConversation, result domain.WhatsAppSendResult) error {
	now := u.clock.Now().UTC()
	touched := applyOutboundToConversation(outboundConversationMessageParams{
		Conversation: conversation,
		TenantID:     conversation.TenantID,
		Recipient:    conversation.DriverPhone,
		MessageType:  "text",
		Text:         message.Text,
	}, now, u.idGenerator)
	savedConversation, err := u.repository.SaveConversation(ctx, touched)
	if err != nil {
		return err
	}
	message.ProviderMessageID = strings.TrimSpace(result.MessageID)
	message.Status = "sent"
	message.UpdatedAt = now
	saved, err := u.repository.UpdateMessage(ctx, message)
	if err != nil {
		return err
	}
	u.notifier.MessageStatus(ctx, saved)
	u.notifier.ConversationUpdated(ctx, savedConversation)
	return nil
}
