package application

import (
	"context"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type outboundKind struct {
	MessageType  string
	Text         string
	TemplateName string
	Caption      string
}

// platformOutbound is what every staff send shares: storing the row once per
// client id, and finishing it as sent or failed without rolling back anything
// that arrived while the provider call was in flight.
type platformOutbound struct {
	repository  PlatformMessagingRepository
	notifier    *PlatformMessagingNotifier
	clock       Clock
	idGenerator IDGenerator
}

func (o platformOutbound) newMessage(conversation domain.WhatsAppConversation, clientMessageID, actorID, status string) domain.WhatsAppConversationMessage {
	now := o.clock.Now().UTC()
	return domain.WhatsAppConversationMessage{
		ID:              o.idGenerator.New("wam"),
		TenantID:        conversation.TenantID,
		ConversationID:  conversation.ID,
		ClientMessageID: clientMessageID,
		Direction:       domain.WhatsAppDirectionOutbound,
		Status:          status,
		SenderRole:      platformStaffRole,
		SenderSubject:   actorID,
		ToPhone:         conversation.DriverPhone,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// saveNew stores the row, treating a concurrent insert of the same client id
// as a replay of the winner.
func (o platformOutbound) saveNew(ctx context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, bool, error) {
	saved, err := o.repository.SaveMessage(ctx, message)
	if err == nil {
		return saved, false, nil
	}
	existing, found, findErr := o.repository.FindMessageByClientID(ctx, message.TenantID, message.ClientMessageID)
	if findErr != nil || !found {
		return domain.WhatsAppConversationMessage{}, false, err
	}
	return existing, true, o.replay(ctx, existing)
}

func (o platformOutbound) replay(ctx context.Context, message domain.WhatsAppConversationMessage) error {
	conversation, ok, err := o.repository.FindConversationByTenantAndID(ctx, message.TenantID, message.ConversationID)
	if err != nil {
		return err
	}
	o.notifier.MessageCreated(ctx, message)
	if ok {
		o.notifier.ConversationUpdated(ctx, conversation)
	}
	return nil
}

func (o platformOutbound) markFailed(ctx context.Context, message domain.WhatsAppConversationMessage, conversation domain.WhatsAppConversation, cause error) error {
	return o.fail(ctx, message, conversation, sendFailurePayload(cause))
}

func (o platformOutbound) fail(ctx context.Context, message domain.WhatsAppConversationMessage, conversation domain.WhatsAppConversation, failure map[string]any) error {
	message.Status = "failed"
	message.Payload = mergeAnyMap(message.Payload, map[string]any{"error": failure})
	message.UpdatedAt = o.clock.Now().UTC()
	saved, err := o.repository.UpdateMessage(ctx, message)
	if err != nil {
		return err
	}
	o.notifier.MessageStatus(ctx, saved)
	o.notifier.ConversationUpdated(ctx, conversation)
	return nil
}

// markSent reloads the conversation and the row first: an inbound or a
// delivery receipt that landed during the provider call must survive.
func (o platformOutbound) markSent(ctx context.Context, message domain.WhatsAppConversationMessage, kind outboundKind, result domain.WhatsAppSendResult) error {
	fresh, ok, err := o.repository.FindConversationByTenantAndID(ctx, message.TenantID, message.ConversationID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWhatsAppConversationNotFound
	}
	now := o.clock.Now().UTC()
	touched, err := o.repository.SaveConversation(ctx, applyOutboundToConversation(outboundConversationMessageParams{
		Conversation: fresh,
		TenantID:     fresh.TenantID,
		Recipient:    fresh.DriverPhone,
		MessageType:  kind.MessageType,
		Text:         kind.Text,
		TemplateName: kind.TemplateName,
		Caption:      kind.Caption,
		SentTemplate: kind.MessageType == "template",
	}, now, o.idGenerator))
	if err != nil {
		return err
	}
	if current, found, findErr := o.repository.FindMessageByClientID(ctx, message.TenantID, message.ClientMessageID); findErr == nil && found {
		message.Status = current.Status
		message.Payload = mergeAnyMap(message.Payload, current.Payload)
	}
	message.ProviderMessageID = strings.TrimSpace(result.MessageID)
	message.Status = advancedStatus(inboxStatus(message), "sent")
	message.UpdatedAt = now
	saved, err := o.repository.UpdateMessage(ctx, message)
	if err != nil {
		return err
	}
	o.notifier.MessageStatus(ctx, saved)
	o.notifier.ConversationUpdated(ctx, touched)
	return nil
}
