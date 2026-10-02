package application

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

const (
	minScheduleLead  = time.Minute
	maxScheduleAhead = 30 * 24 * time.Hour
	dueBatchSize     = 25
)

type ScheduleCommand struct {
	ClientMessageID string
	ConversationID  string
	Text            string
	SendAt          time.Time
	ActorID         string
}

// PlatformScheduledSends holds text replies staff set to go out later. The
// 24-hour window is checked when the message is due, not when it is saved,
// because the customer may write again before then.
type PlatformScheduledSends struct {
	send       *PlatformMessagingSendUseCase
	repository PlatformInboxAdminRepository
}

func NewPlatformScheduledSends(send *PlatformMessagingSendUseCase, repository PlatformInboxAdminRepository) *PlatformScheduledSends {
	return &PlatformScheduledSends{send: send, repository: repository}
}

func (u *PlatformScheduledSends) Schedule(ctx context.Context, command ScheduleCommand) (MessageView, error) {
	text := strings.TrimSpace(command.Text)
	now := u.send.clock.Now()
	switch {
	case !validID(command.ClientMessageID) || !validID(command.ConversationID):
		return MessageView{}, InboxInputError("conversation and client message id are required")
	case text == "" || utf8.RuneCountInString(text) > maxPlatformMessageText || hasDisallowedControl(text):
		return MessageView{}, InboxInputError("a message must be 1 to 4096 characters")
	case command.SendAt.Before(now.Add(minScheduleLead)) || command.SendAt.After(now.Add(maxScheduleAhead)):
		return MessageView{}, InboxInputError("choose a time between 1 minute and 30 days from now")
	}
	if existing, found, err := u.repository.FindMessageByClientID(ctx, identity.SystemTenantID, command.ClientMessageID); err != nil || found {
		return NewMessageView(existing), err
	}
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, identity.SystemTenantID, strings.TrimSpace(command.ConversationID))
	if err != nil {
		return MessageView{}, err
	}
	if !ok {
		return MessageView{}, ErrWhatsAppConversationNotFound
	}
	message := u.send.newTextMessage(conversation, PlatformSendCommand{ClientMessageID: command.ClientMessageID, Text: text, ActorID: command.ActorID}, domain.WhatsAppMessageStatusScheduled)
	message.Payload["scheduled_at"] = command.SendAt.UTC().Format(time.RFC3339)
	saved, duplicate, err := u.send.saveNew(ctx, message)
	if err != nil || duplicate {
		return NewMessageView(saved), err
	}
	u.send.notifier.MessageCreated(ctx, saved)
	return NewMessageView(saved), nil
}

func (u *PlatformScheduledSends) Cancel(ctx context.Context, messageID string) error {
	deleted, ok, err := u.repository.DeleteScheduledMessage(ctx, identity.SystemTenantID, strings.TrimSpace(messageID))
	if err != nil {
		return err
	}
	if !ok {
		return InboxInputError("only a message that has not been sent yet can be cancelled")
	}
	u.send.notifier.MessageDeleted(ctx, deleted)
	return nil
}

// RunDue sends every scheduled message whose time has come and returns how
// many it picked up. Claiming is atomic, so two instances never send twice.
func (u *PlatformScheduledSends) RunDue(ctx context.Context) (int, error) {
	now := u.send.clock.Now()
	due, err := u.repository.ListDueScheduledMessages(ctx, identity.SystemTenantID, now, dueBatchSize)
	if err != nil {
		return 0, err
	}
	handled := 0
	var failures error
	for _, scheduled := range due {
		claimed, ok, err := u.repository.ClaimScheduledMessage(ctx, scheduled.TenantID, scheduled.ID, now)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if ok {
			handled++
			failures = errors.Join(failures, u.dispatch(ctx, claimed))
		}
	}
	return handled, failures
}

func (u *PlatformScheduledSends) dispatch(ctx context.Context, message domain.WhatsAppConversationMessage) error {
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, message.TenantID, message.ConversationID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	u.send.notifier.MessageCreated(ctx, message)
	if conversation.IsLocked(u.send.clock.Now()) {
		return u.send.fail(ctx, message, conversation, errorPayload(ErrorCodeWindowLocked, ""))
	}
	return u.send.deliverText(ctx, message, conversation)
}
