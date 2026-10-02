package application

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

var (
	ErrOutreachRecipientRequired = errors.New("conversation id or phone number is required")
	ErrOutreachPhoneInvalid      = errors.New("phone number must be 8 to 15 digits with country code")

	outreachPhonePattern = regexp.MustCompile(`^[0-9]{8,15}$`)
)

type PlatformTemplateSendCommand struct {
	ClientMessageID string
	ConversationID  string
	Phone           string
	Name            string
	TemplateName    string
	Language        string
	Variables       map[string]string
	ActorID         string
}

type PlatformTemplateSendResult struct {
	Conversation ConversationView `json:"conversation"`
	Message      MessageView      `json:"message"`
}

// PlatformTemplateSendUseCase lets staff start a conversation, or reopen one
// whose 24-hour window closed, by sending an approved template from the
// platform number.
type PlatformTemplateSendUseCase struct {
	repository  PlatformMessagingRepository
	templates   WhatsAppTemplateProvider
	sender      WhatsAppTemplateMessageProvider
	notifier    *PlatformMessagingNotifier
	clock       Clock
	idGenerator IDGenerator
}

func NewPlatformTemplateSendUseCase(repository PlatformMessagingRepository, templates WhatsAppTemplateProvider, sender WhatsAppTemplateMessageProvider, notifier *PlatformMessagingNotifier, clock Clock, idGenerator IDGenerator) *PlatformTemplateSendUseCase {
	return &PlatformTemplateSendUseCase{repository: repository, templates: templates, sender: sender, notifier: notifier, clock: clock, idGenerator: idGenerator}
}

func (u *PlatformTemplateSendUseCase) Execute(ctx context.Context, command PlatformTemplateSendCommand) (PlatformTemplateSendResult, error) {
	if strings.TrimSpace(command.TemplateName) == "" {
		return PlatformTemplateSendResult{}, ErrOtherTemplateTemplateNameRequired
	}
	if replay, found, err := u.replayExisting(ctx, command.ClientMessageID); err != nil || found {
		return replay, err
	}
	conversation, err := u.resolveConversation(ctx, command)
	if err != nil {
		return PlatformTemplateSendResult{}, err
	}
	template, components, err := u.prepareTemplate(ctx, command)
	if err != nil {
		return PlatformTemplateSendResult{}, err
	}
	language := firstNonEmpty(command.Language, template.Language, "en_US")
	queued, err := u.repository.SaveMessage(ctx, u.newMessage(conversation, command, template, language))
	if err != nil {
		return PlatformTemplateSendResult{}, err
	}
	u.notifier.MessageCreated(ctx, queued)
	u.notifier.ConversationUpdated(ctx, conversation)

	result, sendErr := u.sender.SendTemplate(ctx, domain.WhatsAppTemplateMessageRequest{
		TenantID:   identity.SystemTenantID,
		Recipient:  conversation.DriverPhone,
		Name:       template.Name,
		Language:   language,
		Components: components,
	})
	return u.finish(ctx, conversation, queued, result, sendErr)
}

func (u *PlatformTemplateSendUseCase) replayExisting(ctx context.Context, clientMessageID string) (PlatformTemplateSendResult, bool, error) {
	if strings.TrimSpace(clientMessageID) == "" {
		return PlatformTemplateSendResult{}, false, nil
	}
	message, found, err := u.repository.FindMessageByClientID(ctx, identity.SystemTenantID, strings.TrimSpace(clientMessageID))
	if err != nil || !found {
		return PlatformTemplateSendResult{}, false, err
	}
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, identity.SystemTenantID, message.ConversationID)
	if err != nil || !ok {
		return PlatformTemplateSendResult{}, false, err
	}
	return u.result(conversation, message), true, nil
}

func (u *PlatformTemplateSendUseCase) result(conversation domain.WhatsAppConversation, message domain.WhatsAppConversationMessage) PlatformTemplateSendResult {
	return PlatformTemplateSendResult{Conversation: NewConversationView(conversation, u.clock.Now()), Message: NewMessageView(message)}
}

func (u *PlatformTemplateSendUseCase) resolveConversation(ctx context.Context, command PlatformTemplateSendCommand) (domain.WhatsAppConversation, error) {
	if id := strings.TrimSpace(command.ConversationID); id != "" {
		conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, identity.SystemTenantID, id)
		if err != nil {
			return domain.WhatsAppConversation{}, err
		}
		if !ok {
			return domain.WhatsAppConversation{}, ErrWhatsAppConversationNotFound
		}
		return conversation, nil
	}
	phone := normalizeConversationPhone(command.Phone)
	if phone == "" {
		return domain.WhatsAppConversation{}, ErrOutreachRecipientRequired
	}
	if !outreachPhonePattern.MatchString(phone) {
		return domain.WhatsAppConversation{}, ErrOutreachPhoneInvalid
	}
	existing, ok, err := u.repository.FindConversationByTenantAndDriverPhone(ctx, identity.SystemTenantID, phone)
	if err != nil || ok {
		return existing, err
	}
	now := u.clock.Now().UTC()
	return u.repository.SaveConversation(ctx, domain.WhatsAppConversation{
		ID:          u.idGenerator.New("wac"),
		TenantID:    identity.SystemTenantID,
		DriverPhone: phone,
		DriverName:  strings.TrimSpace(command.Name),
		CreatedAt:   now,
		UpdatedAt:   now,
	})
}

func (u *PlatformTemplateSendUseCase) prepareTemplate(ctx context.Context, command PlatformTemplateSendCommand) (domain.WhatsAppTemplate, []map[string]any, error) {
	template, err := findWhatsAppTemplateByName(ctx, u.templates, identity.SystemTenantID, strings.TrimSpace(command.TemplateName))
	if err != nil {
		if errors.Is(err, ErrJobInstructionTemplateNotFound) {
			return domain.WhatsAppTemplate{}, nil, ErrOtherTemplateTemplateNotFound
		}
		return domain.WhatsAppTemplate{}, nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(template.Status), "APPROVED") {
		return domain.WhatsAppTemplate{}, nil, ErrOtherTemplateTemplateNotApproved
	}
	components, err := buildOtherTemplateSendComponents(template, SendOtherTemplateCommand{Variables: command.Variables})
	return template, components, err
}

func (u *PlatformTemplateSendUseCase) newMessage(conversation domain.WhatsAppConversation, command PlatformTemplateSendCommand, template domain.WhatsAppTemplate, language string) domain.WhatsAppConversationMessage {
	now := u.clock.Now().UTC()
	clientID := strings.TrimSpace(command.ClientMessageID)
	if clientID == "" {
		clientID = u.idGenerator.New("cm")
	}
	return domain.WhatsAppConversationMessage{
		ID:              u.idGenerator.New("wam"),
		TenantID:        conversation.TenantID,
		ConversationID:  conversation.ID,
		ClientMessageID: clientID,
		Direction:       domain.WhatsAppDirectionOutbound,
		MessageType:     "template",
		Status:          "queued",
		SenderRole:      "platform_staff",
		SenderSubject:   command.ActorID,
		ToPhone:         conversation.DriverPhone,
		Text:            renderTemplateBody(template, command.Variables),
		TemplateName:    template.Name,
		Payload:         map[string]any{"type": "template", "template_name": template.Name, "language": language, "variables": command.Variables},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func (u *PlatformTemplateSendUseCase) finish(ctx context.Context, conversation domain.WhatsAppConversation, queued domain.WhatsAppConversationMessage, result domain.WhatsAppSendResult, sendErr error) (PlatformTemplateSendResult, error) {
	if sendErr != nil {
		return u.markFailed(ctx, conversation, queued, sendErr)
	}
	return u.markSent(ctx, queued, result)
}

func (u *PlatformTemplateSendUseCase) markFailed(ctx context.Context, conversation domain.WhatsAppConversation, message domain.WhatsAppConversationMessage, sendErr error) (PlatformTemplateSendResult, error) {
	message.Status = "failed"
	message.Payload = mergeAnyMap(message.Payload, map[string]any{"error": errorPayload(ErrorCodeSendFailed, domain.PlainErrorText(sendErr))})
	message.UpdatedAt = u.clock.Now().UTC()
	saved, err := u.repository.UpdateMessage(ctx, message)
	if err != nil {
		return PlatformTemplateSendResult{}, err
	}
	u.notifier.MessageStatus(ctx, saved)
	return u.result(conversation, saved), nil
}

// markSent reloads the conversation before touching it, so an inbound that
// arrived during the Meta call is not rolled back by a stale copy.
func (u *PlatformTemplateSendUseCase) markSent(ctx context.Context, message domain.WhatsAppConversationMessage, result domain.WhatsAppSendResult) (PlatformTemplateSendResult, error) {
	fresh, ok, err := u.repository.FindConversationByTenantAndID(ctx, message.TenantID, message.ConversationID)
	if err != nil {
		return PlatformTemplateSendResult{}, err
	}
	if !ok {
		return PlatformTemplateSendResult{}, ErrWhatsAppConversationNotFound
	}
	now := u.clock.Now().UTC()
	touched, err := u.repository.SaveConversation(ctx, applyOutboundToConversation(outboundConversationMessageParams{
		Conversation: fresh,
		TenantID:     fresh.TenantID,
		Recipient:    fresh.DriverPhone,
		MessageType:  "template",
		TemplateName: message.TemplateName,
		SentTemplate: true,
	}, now, u.idGenerator))
	if err != nil {
		return PlatformTemplateSendResult{}, err
	}
	message.ProviderMessageID = strings.TrimSpace(result.MessageID)
	message.Status = advancedStatus(inboxStatus(message), "sent")
	message.UpdatedAt = now
	saved, err := u.repository.UpdateMessage(ctx, message)
	if err != nil {
		return PlatformTemplateSendResult{}, err
	}
	u.notifier.MessageStatus(ctx, saved)
	u.notifier.ConversationUpdated(ctx, touched)
	return u.result(touched, saved), nil
}

// renderTemplateBody fills the template's body so the inbox shows what the
// driver received.
func renderTemplateBody(template domain.WhatsAppTemplate, variables map[string]string) string {
	values := normalizeTemplateVariables(variables)
	for _, component := range template.Components {
		if !strings.EqualFold(stringValue(component["type"]), "BODY") {
			continue
		}
		return whatsAppTemplatePlaceholderPattern.ReplaceAllStringFunc(stringValue(component["text"]), func(match string) string {
			index, err := strconv.Atoi(whatsAppTemplatePlaceholderPattern.FindStringSubmatch(match)[1])
			if err != nil {
				return match
			}
			if value, ok := values[index]; ok {
				return value
			}
			return match
		})
	}
	return "Template: " + template.Name
}
