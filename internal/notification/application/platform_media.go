package application

import (
	"context"
	"errors"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

var ErrPlatformMediaNotFound = errors.New("media not found")

const maxMediaCaptionLength = 1024

type mediaRule struct {
	kind     string
	maxBytes int
}

const (
	megabyte = 1024 * 1024

	MaxPlatformMediaBytes = 20 * megabyte
)

// Meta accepts only these types; sizes follow its published limits, with
// documents held to what a request can reasonably carry.
var platformMediaRules = map[string]mediaRule{
	"image/jpeg":                    {"image", 5 * megabyte},
	"image/png":                     {"image", 5 * megabyte},
	"video/mp4":                     {"video", 16 * megabyte},
	"video/3gpp":                    {"video", 16 * megabyte},
	"audio/aac":                     {"audio", 16 * megabyte},
	"audio/mp4":                     {"audio", 16 * megabyte},
	"audio/mpeg":                    {"audio", 16 * megabyte},
	"audio/amr":                     {"audio", 16 * megabyte},
	"audio/ogg":                     {"audio", 16 * megabyte},
	"application/pdf":               {"document", MaxPlatformMediaBytes},
	"application/msword":            {"document", MaxPlatformMediaBytes},
	"text/plain":                    {"document", MaxPlatformMediaBytes},
	"application/vnd.ms-excel":      {"document", MaxPlatformMediaBytes},
	"application/vnd.ms-powerpoint": {"document", MaxPlatformMediaBytes},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   {"document", MaxPlatformMediaBytes},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         {"document", MaxPlatformMediaBytes},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {"document", MaxPlatformMediaBytes},
}

type PlatformMediaCommand struct {
	ClientMessageID string
	ConversationID  string
	Filename        string
	ContentType     string
	Data            []byte
	Caption         string
	ActorID         string
}

// PlatformMediaSendUseCase sends a staff attachment from the platform number.
// Like text, it is refused once the 24-hour window has closed.
type PlatformMediaSendUseCase struct {
	platformOutbound
	media PlatformMediaProvider
}

func NewPlatformMediaSendUseCase(repository PlatformMessagingRepository, media PlatformMediaProvider, notifier *PlatformMessagingNotifier, clock Clock, idGenerator IDGenerator) *PlatformMediaSendUseCase {
	return &PlatformMediaSendUseCase{
		platformOutbound: platformOutbound{repository: repository, notifier: notifier, clock: clock, idGenerator: idGenerator},
		media:            media,
	}
}

func (u *PlatformMediaSendUseCase) Execute(ctx context.Context, command PlatformMediaCommand) (MessageView, error) {
	rule, contentType, err := checkPlatformMedia(command)
	if err != nil {
		return MessageView{}, err
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
	if conversation.IsLocked(u.clock.Now()) {
		return MessageView{}, ErrWhatsAppConversationLocked
	}
	mediaID, err := u.media.UploadMedia(ctx, conversation.TenantID, command.Filename, contentType, command.Data)
	if err != nil {
		return MessageView{}, err
	}
	return u.sendUploaded(ctx, conversation, command, rule.kind, mediaID)
}

func (u *PlatformMediaSendUseCase) sendUploaded(ctx context.Context, conversation domain.WhatsAppConversation, command PlatformMediaCommand, kind, mediaID string) (MessageView, error) {
	caption := ""
	if kind != "audio" {
		caption = strings.TrimSpace(command.Caption)
	}
	message := u.newMessage(conversation, command.ClientMessageID, command.ActorID, "queued")
	message.MessageType = kind
	message.MediaType = kind
	message.MediaID = mediaID
	message.Caption = caption
	message.Payload = map[string]any{"type": kind, "media_id": mediaID, "filename": command.Filename}
	queued, duplicate, err := u.saveNew(ctx, message)
	if err != nil || duplicate {
		return NewMessageView(queued), err
	}
	u.notifier.MessageCreated(ctx, queued)
	u.notifier.ConversationUpdated(ctx, conversation)

	result, sendErr := u.media.SendMedia(ctx, domain.WhatsAppMediaMessageRequest{
		TenantID:  conversation.TenantID,
		Recipient: conversation.DriverPhone,
		MediaType: kind,
		MediaID:   mediaID,
		Caption:   caption,
		Filename:  command.Filename,
	})
	return u.finish(ctx, conversation, queued, outboundKind{MessageType: kind, Caption: caption, Text: command.Filename}, result, sendErr)
}

func (u *PlatformMediaSendUseCase) finish(ctx context.Context, conversation domain.WhatsAppConversation, queued domain.WhatsAppConversationMessage, kind outboundKind, result domain.WhatsAppSendResult, sendErr error) (MessageView, error) {
	if sendErr != nil {
		err := u.markFailed(ctx, queued, conversation, sendErr)
		queued.Status = "failed"
		return NewMessageView(queued), err
	}
	if err := u.markSent(ctx, queued, kind, result); err != nil {
		return MessageView{}, err
	}
	saved, _, err := u.repository.FindMessageByClientID(ctx, queued.TenantID, queued.ClientMessageID)
	return NewMessageView(saved), err
}

func checkPlatformMedia(command PlatformMediaCommand) (mediaRule, string, error) {
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(command.ContentType))
	rule, supported := platformMediaRules[strings.ToLower(contentType)]
	switch {
	case !validID(command.ClientMessageID) || !validID(command.ConversationID):
		return mediaRule{}, "", InboxInputError("conversation and client message id are required")
	case err != nil || !supported:
		return mediaRule{}, "", InboxInputError("this file type cannot be sent on WhatsApp")
	case len(command.Data) == 0:
		return mediaRule{}, "", InboxInputError("the file is empty")
	case len(command.Data) > rule.maxBytes:
		return mediaRule{}, "", InboxInputError("this file is too large for WhatsApp")
	case utf8.RuneCountInString(command.Caption) > maxMediaCaptionLength:
		return mediaRule{}, "", InboxInputError("caption must be at most 1024 characters")
	case strings.TrimSpace(command.Filename) == "" || filepath.Base(command.Filename) != command.Filename:
		return mediaRule{}, "", InboxInputError("file name is not valid")
	}
	return rule, strings.ToLower(contentType), nil
}

type PlatformMedia struct {
	Data        []byte
	ContentType string
	Filename    string
}

// PlatformMediaFetchUseCase streams a message's media to staff. The inbox
// only ever holds Meta's media id, so the bytes are fetched on demand.
type PlatformMediaFetchUseCase struct {
	repository PlatformInboxAdminRepository
	media      PlatformMediaProvider
}

func NewPlatformMediaFetchUseCase(repository PlatformInboxAdminRepository, media PlatformMediaProvider) *PlatformMediaFetchUseCase {
	return &PlatformMediaFetchUseCase{repository: repository, media: media}
}

func (u *PlatformMediaFetchUseCase) Execute(ctx context.Context, messageID string) (PlatformMedia, error) {
	message, ok, err := u.repository.FindMessageByID(ctx, identity.SystemTenantID, messageID)
	if err != nil {
		return PlatformMedia{}, err
	}
	if !ok || strings.TrimSpace(message.MediaID) == "" {
		return PlatformMedia{}, ErrPlatformMediaNotFound
	}
	data, contentType, err := u.media.DownloadMedia(ctx, identity.SystemTenantID, message.MediaID)
	if err != nil {
		return PlatformMedia{}, err
	}
	filename, _ := message.Payload["filename"].(string)
	return PlatformMedia{Data: data, ContentType: contentType, Filename: filename}, nil
}
