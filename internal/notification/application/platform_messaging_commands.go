package application

import (
	"context"
	"log/slog"
	"strings"
	"unicode"

	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/identity"
)

const (
	PlatformSendRequestedSubject = "tm.system.messaging.send.requested"
	PlatformReadRequestedSubject = "tm.system.messaging.read.requested"

	maxPlatformMessageText = 4096
	maxPlatformIDLength    = 128
	manageMessagingPerm    = "admin.messaging.manage"
)

// PlatformMessagingCommands handles the signed commands the realtime gateway
// relays. The envelope is verified by the bus wrapper before it gets here; the
// acting user is checked again, so a signed command still needs a real staff
// member who may reply.
type PlatformMessagingCommands struct {
	send       *PlatformMessagingSendUseCase
	read       *PlatformMessagingReadUseCase
	authorizer PlatformStaffAuthorizer
	repository PlatformMessagingRepository
	notifier   *PlatformMessagingNotifier
	log        *slog.Logger
}

func NewPlatformMessagingCommands(send *PlatformMessagingSendUseCase, read *PlatformMessagingReadUseCase, authorizer PlatformStaffAuthorizer, repository PlatformMessagingRepository, notifier *PlatformMessagingNotifier, log *slog.Logger) *PlatformMessagingCommands {
	return &PlatformMessagingCommands{send: send, read: read, authorizer: authorizer, repository: repository, notifier: notifier, log: log}
}

func (c *PlatformMessagingCommands) HandleSend(ctx context.Context, envelope eventbus.EventEnvelope) error {
	command, ok := parsePlatformSend(envelope)
	if !ok {
		c.log.Warn("platform_messaging_command_dropped", "subject", envelope.Subject, "reason", "invalid")
		c.reportInvalid(ctx, command)
		return nil
	}
	if !c.authorizer.CanManageMessaging(ctx, command.ActorID) {
		c.refuse(ctx, command)
		return nil
	}
	return c.send.Execute(ctx, command)
}

func (c *PlatformMessagingCommands) HandleRead(ctx context.Context, envelope eventbus.EventEnvelope) error {
	actor, conversationID := actorOf(envelope), stringField(envelope.Data, "conversation_id")
	if envelope.TenantID != identity.SystemTenantID || actor == "" || !validID(conversationID) {
		c.log.Warn("platform_messaging_command_dropped", "subject", envelope.Subject, "reason", "invalid")
		return nil
	}
	if !c.authorizer.CanManageMessaging(ctx, actor) {
		c.log.Warn("platform_messaging_command_refused", "subject", envelope.Subject, "actor", actor)
		return nil
	}
	return c.read.Execute(ctx, conversationID)
}

func (c *PlatformMessagingCommands) reportInvalid(ctx context.Context, command PlatformSendCommand) {
	if command.ActorID == "" || !validID(command.ClientMessageID) || !validID(command.ConversationID) {
		return
	}
	c.notifier.Failure(ctx, command.ConversationID, command.ClientMessageID, ErrorCodeSendFailed)
}

func (c *PlatformMessagingCommands) refuse(ctx context.Context, command PlatformSendCommand) {
	c.log.Warn("platform_messaging_command_refused", "actor", command.ActorID, "client_message_id", command.ClientMessageID)
	existing, found, err := c.repository.FindMessageByClientID(ctx, identity.SystemTenantID, command.ClientMessageID)
	if err != nil || !found {
		return
	}
	c.notifier.Failure(ctx, existing.ConversationID, command.ClientMessageID, ErrorCodeForbidden)
}

func parsePlatformSend(envelope eventbus.EventEnvelope) (PlatformSendCommand, bool) {
	command := PlatformSendCommand{
		ClientMessageID: stringField(envelope.Data, "client_message_id"),
		ConversationID:  stringField(envelope.Data, "conversation_id"),
		Text:            strings.TrimSpace(stringField(envelope.Data, "text")),
		ActorID:         actorOf(envelope),
	}
	valid := envelope.TenantID == identity.SystemTenantID && command.ActorID != "" &&
		validID(command.ClientMessageID) && validID(command.ConversationID) &&
		command.Text != "" && runeCount(command.Text) <= maxPlatformMessageText &&
		!hasDisallowedControl(command.Text)
	return command, valid
}

func hasDisallowedControl(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	})
}

func actorOf(envelope eventbus.EventEnvelope) string {
	return stringField(envelope.Actor, "user_id")
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func validID(value string) bool {
	return value != "" && len(value) <= maxPlatformIDLength
}

func runeCount(value string) int {
	return len([]rune(value))
}

// AccessLookup is the part of the auth access client the authorizer needs.
type AccessLookup interface {
	Get(ctx context.Context, userID string) (identity.Access, identity.AccessLookup)
}

// AccessAuthorizer allows an active platform user holding admin.messaging.manage.
type AccessAuthorizer struct {
	access AccessLookup
}

func NewAccessAuthorizer(access AccessLookup) *AccessAuthorizer {
	return &AccessAuthorizer{access: access}
}

func (a *AccessAuthorizer) CanManageMessaging(ctx context.Context, userID string) bool {
	access, lookup := a.access.Get(ctx, userID)
	if lookup != identity.AccessFound || !access.Active {
		return false
	}
	if access.Portal != identity.PortalAdmin || access.TenantID != identity.SystemTenantID {
		return false
	}
	for _, permission := range access.Permissions {
		if permission == manageMessagingPerm {
			return true
		}
	}
	return false
}
