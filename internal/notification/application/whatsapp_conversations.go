package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const whatsAppConversationWindow = 24 * time.Hour

var (
	ErrWhatsAppConversationNotFound   = errors.New("whatsapp conversation not found")
	ErrWhatsAppConversationLocked     = errors.New("conversation is locked until the driver replies to a template message")
	ErrWhatsAppMessageRequired        = errors.New("message is required")
	ErrWhatsAppMediaTypeRequired      = errors.New("media type is required")
	ErrWhatsAppMediaTypeUnsupported   = errors.New("media type must be one of: image, video, audio, document")
	ErrWhatsAppMediaValueRequired     = errors.New("media_id or media_link is required")
	ErrWhatsAppConversationIDRequired = errors.New("conversation id is required")
)

type ListWhatsAppConversationsQuery struct {
	TenantID string
	Query    string
	State    string
}

type GetWhatsAppConversationQuery struct {
	TenantID       string
	ConversationID string
}

type ListWhatsAppConversationMessagesQuery struct {
	TenantID       string
	ConversationID string
}

type SendWhatsAppTextMessageCommand struct {
	TenantID  string
	Recipient string
	Message   string
}

type SendWhatsAppMediaMessageCommand struct {
	TenantID  string
	Recipient string
	MediaType string
	MediaID   string
	MediaLink string
	Caption   string
	Filename  string
}

type HandleWhatsAppWebhookCommand struct {
	Payload []byte
}

type SendWhatsAppTextMessageUseCase struct {
	repository             OutboxRepository
	conversationRepository WhatsAppConversationRepository
	provider               WhatsAppConversationMessageProvider
	eventPublisher         DeliveryEventPublisher
	clock                  Clock
	idGenerator            IDGenerator
	defaultTenant          string
}

type SendWhatsAppMediaMessageUseCase struct {
	repository             OutboxRepository
	conversationRepository WhatsAppConversationRepository
	provider               WhatsAppConversationMessageProvider
	eventPublisher         DeliveryEventPublisher
	clock                  Clock
	idGenerator            IDGenerator
	defaultTenant          string
}

type ListWhatsAppConversationsUseCase struct {
	repository    WhatsAppConversationRepository
	clock         Clock
	defaultTenant string
}

type GetWhatsAppConversationUseCase struct {
	repository    WhatsAppConversationRepository
	defaultTenant string
}

type ListWhatsAppConversationMessagesUseCase struct {
	repository    WhatsAppConversationRepository
	defaultTenant string
}

type HandleWhatsAppWebhookUseCase struct {
	repository                WhatsAppConversationRepository
	outboxRepository          OutboxRepository
	messageProvider           MessageProvider
	driverStatusRequestSender DriverStatusRequestSender
	eventPublisher            DeliveryEventPublisher
	clock                     Clock
	idGenerator               IDGenerator
}

func NewSendWhatsAppTextMessageUseCase(repository OutboxRepository, conversationRepository WhatsAppConversationRepository, provider WhatsAppConversationMessageProvider, eventPublisher DeliveryEventPublisher, clock Clock, idGenerator IDGenerator, defaultTenant string) *SendWhatsAppTextMessageUseCase {
	return &SendWhatsAppTextMessageUseCase{repository: repository, conversationRepository: conversationRepository, provider: provider, eventPublisher: eventPublisher, clock: clock, idGenerator: idGenerator, defaultTenant: defaultTenant}
}

func NewSendWhatsAppMediaMessageUseCase(repository OutboxRepository, conversationRepository WhatsAppConversationRepository, provider WhatsAppConversationMessageProvider, eventPublisher DeliveryEventPublisher, clock Clock, idGenerator IDGenerator, defaultTenant string) *SendWhatsAppMediaMessageUseCase {
	return &SendWhatsAppMediaMessageUseCase{repository: repository, conversationRepository: conversationRepository, provider: provider, eventPublisher: eventPublisher, clock: clock, idGenerator: idGenerator, defaultTenant: defaultTenant}
}

func NewListWhatsAppConversationsUseCase(repository WhatsAppConversationRepository, clock Clock, defaultTenant string) *ListWhatsAppConversationsUseCase {
	return &ListWhatsAppConversationsUseCase{repository: repository, clock: clock, defaultTenant: defaultTenant}
}

func NewGetWhatsAppConversationUseCase(repository WhatsAppConversationRepository, defaultTenant string) *GetWhatsAppConversationUseCase {
	return &GetWhatsAppConversationUseCase{repository: repository, defaultTenant: defaultTenant}
}

func NewListWhatsAppConversationMessagesUseCase(repository WhatsAppConversationRepository, defaultTenant string) *ListWhatsAppConversationMessagesUseCase {
	return &ListWhatsAppConversationMessagesUseCase{repository: repository, defaultTenant: defaultTenant}
}

func NewHandleWhatsAppWebhookUseCase(repository WhatsAppConversationRepository, outboxRepository OutboxRepository, messageProvider MessageProvider, driverStatusRequestSender DriverStatusRequestSender, eventPublisher DeliveryEventPublisher, clock Clock, idGenerator IDGenerator) *HandleWhatsAppWebhookUseCase {
	return &HandleWhatsAppWebhookUseCase{
		repository:                repository,
		outboxRepository:          outboxRepository,
		messageProvider:           messageProvider,
		driverStatusRequestSender: driverStatusRequestSender,
		eventPublisher:            eventPublisher,
		clock:                     clock,
		idGenerator:               idGenerator,
	}
}

func (u *SendWhatsAppTextMessageUseCase) Execute(ctx context.Context, command SendWhatsAppTextMessageCommand) (domain.OutboxMessage, domain.WhatsAppConversation, domain.WhatsAppConversationMessage, error) {
	tenantID, err := resolveNotificationTenant(command.TenantID, u.defaultTenant)
	if err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, err
	}
	if strings.TrimSpace(command.Recipient) == "" {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, ErrOtherTemplateRecipientRequired
	}
	if strings.TrimSpace(command.Message) == "" {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, ErrWhatsAppMessageRequired
	}

	conversation, err := requireOpenConversation(ctx, u.conversationRepository, tenantID, command.Recipient, u.clock.Now())
	if err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, err
	}

	now := u.clock.Now()
	outboxMessage, err := domain.NewOutboxMessage(domain.OutboxMessage{
		ID:        u.idGenerator.New("msg"),
		TenantID:  tenantID,
		Channel:   "whatsapp",
		Recipient: normalizeConversationPhone(command.Recipient),
		Content:   strings.TrimSpace(command.Message),
		Status:    domain.StatusQueued,
		Retries:   0,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, fmt.Errorf("build outbox message: %w", err)
	}
	if err := u.repository.Save(outboxMessage); err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, fmt.Errorf("save queued outbox message: %w", err)
	}

	result, sendErr := u.provider.SendText(ctx, command.Recipient, command.Message)
	if sendErr != nil {
		outboxMessage = outboxMessage.MarkFailed(u.clock.Now())
	} else {
		outboxMessage = outboxMessage.MarkSent(result.Status, result.MessageID, u.clock.Now())
	}
	if err := u.repository.Save(outboxMessage); err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, fmt.Errorf("save delivered outbox message: %w", err)
	}
	if publishErr := publishNotificationDeliveryEvent(ctx, u.eventPublisher, u.idGenerator, u.clock, outboxMessage, sendErr); publishErr != nil {
		// best effort
	}
	if sendErr != nil {
		return outboxMessage, conversation, domain.WhatsAppConversationMessage{}, sendErr
	}

	updatedConversation, conversationMessage, err := recordOutboundConversationMessage(ctx, u.conversationRepository, u.clock, u.idGenerator, outboundConversationMessageParams{
		Conversation:      conversation,
		TenantID:          tenantID,
		Recipient:         command.Recipient,
		MessageType:       "text",
		Text:              strings.TrimSpace(command.Message),
		ProviderMessageID: result.MessageID,
		Status:            result.Status,
		Payload: map[string]any{
			"type": "text",
			"body": strings.TrimSpace(command.Message),
		},
	})
	if err != nil {
		return outboxMessage, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, err
	}

	return outboxMessage, updatedConversation, conversationMessage, nil
}

func (u *SendWhatsAppMediaMessageUseCase) Execute(ctx context.Context, command SendWhatsAppMediaMessageCommand) (domain.OutboxMessage, domain.WhatsAppConversation, domain.WhatsAppConversationMessage, error) {
	tenantID, err := resolveNotificationTenant(command.TenantID, u.defaultTenant)
	if err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, err
	}
	if strings.TrimSpace(command.Recipient) == "" {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, ErrOtherTemplateRecipientRequired
	}
	mediaType := strings.ToLower(strings.TrimSpace(command.MediaType))
	if mediaType == "" {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, ErrWhatsAppMediaTypeRequired
	}
	if !isSupportedOutboundMediaType(mediaType) {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, ErrWhatsAppMediaTypeUnsupported
	}
	if strings.TrimSpace(command.MediaID) == "" && strings.TrimSpace(command.MediaLink) == "" {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, ErrWhatsAppMediaValueRequired
	}

	conversation, err := requireOpenConversation(ctx, u.conversationRepository, tenantID, command.Recipient, u.clock.Now())
	if err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, err
	}

	content := buildMediaOutboxContent(mediaType, command.MediaID, command.MediaLink, command.Caption)
	now := u.clock.Now()
	outboxMessage, err := domain.NewOutboxMessage(domain.OutboxMessage{
		ID:        u.idGenerator.New("msg"),
		TenantID:  tenantID,
		Channel:   "whatsapp",
		Recipient: normalizeConversationPhone(command.Recipient),
		Content:   content,
		Status:    domain.StatusQueued,
		Retries:   0,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, fmt.Errorf("build outbox message: %w", err)
	}
	if err := u.repository.Save(outboxMessage); err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, fmt.Errorf("save queued outbox message: %w", err)
	}

	result, sendErr := u.provider.SendMedia(ctx, domain.WhatsAppMediaMessageRequest{
		Recipient: command.Recipient,
		MediaType: mediaType,
		MediaID:   strings.TrimSpace(command.MediaID),
		MediaLink: strings.TrimSpace(command.MediaLink),
		Caption:   strings.TrimSpace(command.Caption),
		Filename:  strings.TrimSpace(command.Filename),
	})
	if sendErr != nil {
		outboxMessage = outboxMessage.MarkFailed(u.clock.Now())
	} else {
		outboxMessage = outboxMessage.MarkSent(result.Status, result.MessageID, u.clock.Now())
	}
	if err := u.repository.Save(outboxMessage); err != nil {
		return domain.OutboxMessage{}, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, fmt.Errorf("save delivered outbox message: %w", err)
	}
	if publishErr := publishNotificationDeliveryEvent(ctx, u.eventPublisher, u.idGenerator, u.clock, outboxMessage, sendErr); publishErr != nil {
		// best effort
	}
	if sendErr != nil {
		return outboxMessage, conversation, domain.WhatsAppConversationMessage{}, sendErr
	}

	updatedConversation, conversationMessage, err := recordOutboundConversationMessage(ctx, u.conversationRepository, u.clock, u.idGenerator, outboundConversationMessageParams{
		Conversation:      conversation,
		TenantID:          tenantID,
		Recipient:         command.Recipient,
		MessageType:       mediaType,
		ProviderMessageID: result.MessageID,
		Status:            result.Status,
		MediaType:         mediaType,
		MediaID:           strings.TrimSpace(command.MediaID),
		MediaLink:         strings.TrimSpace(command.MediaLink),
		Caption:           strings.TrimSpace(command.Caption),
		Payload: map[string]any{
			"type":       mediaType,
			"media_id":   strings.TrimSpace(command.MediaID),
			"media_link": strings.TrimSpace(command.MediaLink),
			"caption":    strings.TrimSpace(command.Caption),
		},
	})
	if err != nil {
		return outboxMessage, domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, err
	}

	return outboxMessage, updatedConversation, conversationMessage, nil
}

func (u *ListWhatsAppConversationsUseCase) Execute(ctx context.Context, query ListWhatsAppConversationsQuery) ([]domain.WhatsAppConversation, error) {
	tenantID, err := resolveNotificationTenant(query.TenantID, u.defaultTenant)
	if err != nil {
		return nil, err
	}
	conversations, err := u.repository.ListConversationsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	state := strings.ToLower(strings.TrimSpace(query.State))
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	filtered := make([]domain.WhatsAppConversation, 0, len(conversations))
	for _, conversation := range conversations {
		if needle != "" && !conversationMatchesQuery(conversation, needle) {
			continue
		}
		if state == "open" && conversation.IsLocked(u.clock.Now()) {
			continue
		}
		if state == "locked" && !conversation.IsLocked(u.clock.Now()) {
			continue
		}
		filtered = append(filtered, conversation)
	}
	return filtered, nil
}

func (u *GetWhatsAppConversationUseCase) Execute(ctx context.Context, query GetWhatsAppConversationQuery) (domain.WhatsAppConversation, error) {
	tenantID, err := resolveNotificationTenant(query.TenantID, u.defaultTenant)
	if err != nil {
		return domain.WhatsAppConversation{}, err
	}
	if strings.TrimSpace(query.ConversationID) == "" {
		return domain.WhatsAppConversation{}, ErrWhatsAppConversationIDRequired
	}
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, tenantID, query.ConversationID)
	if err != nil {
		return domain.WhatsAppConversation{}, err
	}
	if !ok {
		return domain.WhatsAppConversation{}, ErrWhatsAppConversationNotFound
	}
	return conversation, nil
}

func (u *ListWhatsAppConversationMessagesUseCase) Execute(ctx context.Context, query ListWhatsAppConversationMessagesQuery) (domain.WhatsAppConversation, []domain.WhatsAppConversationMessage, error) {
	conversation, err := NewGetWhatsAppConversationUseCase(u.repository, u.defaultTenant).Execute(ctx, GetWhatsAppConversationQuery{TenantID: query.TenantID, ConversationID: query.ConversationID})
	if err != nil {
		return domain.WhatsAppConversation{}, nil, err
	}
	messages, err := u.repository.ListMessagesByConversation(ctx, conversation.TenantID, conversation.ID)
	if err != nil {
		return domain.WhatsAppConversation{}, nil, err
	}
	return conversation, messages, nil
}

func (u *HandleWhatsAppWebhookUseCase) Execute(ctx context.Context, command HandleWhatsAppWebhookCommand) (int, error) {
	var payload whatsAppWebhookPayload
	if err := json.Unmarshal(command.Payload, &payload); err != nil {
		return 0, fmt.Errorf("decode whatsapp webhook: %w", err)
	}

	processed := 0
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			contactNames := map[string]string{}
			for _, contact := range change.Value.Contacts {
				waID := normalizeConversationPhone(contact.WAID)
				if waID == "" {
					continue
				}
				contactNames[waID] = strings.TrimSpace(contact.Profile.Name)
			}
			for _, status := range change.Value.Statuses {
				handled, err := u.applyWebhookStatus(ctx, status)
				if err != nil {
					return processed, err
				}
				processed += handled
			}
			for _, message := range change.Value.Messages {
				conversation, ok, err := resolveWebhookConversation(ctx, u.repository, message)
				if err != nil {
					return processed, err
				}
				if !ok {
					continue
				}

				receivedAt := parseWebhookTimestamp(message.Timestamp, u.clock.Now())
				preview, messageType, mediaType, mediaID, mediaLink, caption, payloadMap := extractWebhookMessageDetails(message)
				fromPhone := normalizeConversationPhone(message.From)
				if name := contactNames[fromPhone]; strings.TrimSpace(name) != "" && strings.TrimSpace(conversation.DriverName) == "" {
					conversation.DriverName = strings.TrimSpace(name)
				}
				conversation.LastInboundMessageAt = timePointer(receivedAt)
				conversation.WindowExpiresAt = timePointer(receivedAt.Add(whatsAppConversationWindow))
				conversation.LastMessageAt = timePointer(receivedAt)
				conversation.LastMessagePreview = preview
				conversation.LastMessageDirection = domain.WhatsAppDirectionInbound
				conversation.LastMessageType = messageType
				conversation.UpdatedAt = receivedAt.UTC()
				savedConversation, err := u.repository.SaveConversation(ctx, conversation)
				if err != nil {
					return processed, err
				}

				messageRecord, err := u.repository.SaveMessage(ctx, domain.WhatsAppConversationMessage{
					ID:                u.idGenerator.New("wam"),
					TenantID:          savedConversation.TenantID,
					ConversationID:    savedConversation.ID,
					ProviderMessageID: strings.TrimSpace(message.ID),
					Direction:         domain.WhatsAppDirectionInbound,
					MessageType:       messageType,
					Status:            "received",
					FromPhone:         fromPhone,
					ToPhone:           strings.TrimSpace(change.Value.Metadata.DisplayPhoneNumber),
					Text:              preview,
					MediaType:         mediaType,
					MediaID:           mediaID,
					MediaLink:         mediaLink,
					Caption:           caption,
					Payload:           payloadMap,
					CreatedAt:         receivedAt.UTC(),
					UpdatedAt:         receivedAt.UTC(),
				})
				if err != nil {
					return processed, err
				}
				if keyword, ok := extractDriverStatusKeyword(message, preview, payloadMap); ok {
					updatedMessage, handleErr := u.recordDriverStatusRequestFromWebhook(
						ctx,
						savedConversation,
						messageRecord,
						keyword,
						preview,
						firstNonEmpty(messageContextID(message), ""),
						receivedAt,
					)
					if handleErr == nil {
						messageRecord = updatedMessage
					}
				}
				processed++
			}
		}
	}
	return processed, nil
}

func (u *HandleWhatsAppWebhookUseCase) applyWebhookStatus(ctx context.Context, status whatsAppWebhookStatus) (int, error) {
	providerMessageID := strings.TrimSpace(status.ID)
	if providerMessageID == "" {
		return 0, nil
	}

	handled := false
	receivedAt := parseWebhookTimestamp(status.Timestamp, u.clock.Now())
	normalizedStatus := normalizeWebhookDeliveryStatus(status.Status)
	statusPayload := buildWebhookStatusPayload(status)

	var conversationMessage *domain.WhatsAppConversationMessage
	if existingMessage, ok, err := u.repository.FindMessageByProviderMessageID(ctx, providerMessageID); err != nil {
		return 0, err
	} else if ok {
		existingMessage.Status = normalizedStatus
		existingMessage.Payload = mergeAnyMap(existingMessage.Payload, statusPayload)
		existingMessage.UpdatedAt = receivedAt.UTC()
		savedMessage, err := u.repository.SaveMessage(ctx, existingMessage)
		if err != nil {
			return 0, err
		}
		conversationMessage = &savedMessage
		handled = true
	}

	if outboxMessage, ok := u.outboxRepository.FindByProviderMessageID(providerMessageID); ok {
		outboxMessage = outboxMessage.MarkDeliveryStatus(normalizedStatus, receivedAt)
		if err := u.outboxRepository.Save(outboxMessage); err != nil {
			return 0, err
		}
		if publishErr := publishWebhookDeliveryEvent(ctx, u.eventPublisher, u.idGenerator, receivedAt, outboxMessage, webhookStatusErrorText(status)); publishErr != nil {
			// best effort
		}
		handled = true
		if normalizedStatus == domain.StatusFailed {
			if err := u.trySMSFallbackForFailedJobInstruction(ctx, outboxMessage, conversationMessage, receivedAt, webhookStatusErrorText(status)); err != nil {
				// best effort
			}
		}
	}

	if handled {
		return 1, nil
	}
	return 0, nil
}

type jobInstructionFallbackContent struct {
	DriverName       string
	JobReference     string
	PickupLocation   string
	DeliveryLocation string
	CargoDescription string
	Priority         string
}

func (u *HandleWhatsAppWebhookUseCase) trySMSFallbackForFailedJobInstruction(ctx context.Context, outboxMessage domain.OutboxMessage, conversationMessage *domain.WhatsAppConversationMessage, receivedAt time.Time, failureReason string) error {
	if u.messageProvider == nil || conversationMessage == nil {
		return nil
	}
	if strings.TrimSpace(outboxMessage.Channel) != "whatsapp" || conversationMessage.Direction != domain.WhatsAppDirectionOutbound {
		return nil
	}

	payload := cloneAnyMap(conversationMessage.Payload)
	if fallbackAttempted(payload) {
		return nil
	}

	content, ok := parseJobInstructionFallbackContent(payload, outboxMessage.Content)
	if !ok {
		return nil
	}

	smsBody := buildJobInstructionSMSBody(content)
	smsOutbox, err := domain.NewOutboxMessage(domain.OutboxMessage{
		ID:        u.idGenerator.New("msg"),
		TenantID:  outboxMessage.TenantID,
		Channel:   "sms",
		Recipient: outboxMessage.Recipient,
		Content:   smsBody,
		Status:    domain.StatusQueued,
		Retries:   0,
		CreatedAt: receivedAt.UTC(),
		UpdatedAt: receivedAt.UTC(),
	})
	if err != nil {
		return err
	}
	if err := u.outboxRepository.Save(smsOutbox); err != nil {
		return err
	}

	sendStatus, sendErr := u.messageProvider.Send(ctx, "sms", outboxMessage.Recipient, smsBody)
	if sendErr != nil {
		smsOutbox = smsOutbox.MarkFailed(receivedAt)
	} else {
		smsOutbox = smsOutbox.MarkSent(sendStatus, "", receivedAt)
	}
	if err := u.outboxRepository.Save(smsOutbox); err != nil {
		return err
	}
	if publishErr := publishNotificationDeliveryEvent(ctx, u.eventPublisher, u.idGenerator, fixedClock{at: receivedAt}, smsOutbox, sendErr); publishErr != nil {
		// best effort
	}

	conversationMessage.Payload = mergeAnyMap(payload, map[string]any{
		"sms_fallback_attempted":    true,
		"sms_fallback_status":       smsOutbox.Status,
		"sms_fallback_message_id":   smsOutbox.ID,
		"sms_fallback_attempted_at": receivedAt.UTC().Format(time.RFC3339),
		"sms_fallback_error":        firstNonEmpty(errorText(sendErr), failureReason),
	})
	conversationMessage.UpdatedAt = receivedAt.UTC()
	_, err = u.repository.SaveMessage(ctx, *conversationMessage)
	return err
}

func fallbackAttempted(payload map[string]any) bool {
	attempted, ok := payload["sms_fallback_attempted"].(bool)
	return ok && attempted
}

func parseJobInstructionFallbackContent(payload map[string]any, rawContent string) (jobInstructionFallbackContent, bool) {
	if parsed, ok := jobInstructionContentFromMap(payload); ok {
		return parsed, true
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawContent)), &decoded); err == nil {
		return jobInstructionContentFromMap(decoded)
	}
	return jobInstructionFallbackContent{}, false
}

func jobInstructionContentFromMap(payload map[string]any) (jobInstructionFallbackContent, bool) {
	if len(payload) == 0 {
		return jobInstructionFallbackContent{}, false
	}
	kind := strings.TrimSpace(stringValue(payload["kind"]))
	if kind != "" && kind != "job_instruction" {
		return jobInstructionFallbackContent{}, false
	}

	content := jobInstructionFallbackContent{
		DriverName:       strings.TrimSpace(stringValue(payload["driver_name"])),
		JobReference:     strings.TrimSpace(stringValue(payload["job_reference"])),
		PickupLocation:   strings.TrimSpace(stringValue(payload["pickup_location"])),
		DeliveryLocation: strings.TrimSpace(stringValue(payload["delivery_location"])),
		CargoDescription: strings.TrimSpace(stringValue(payload["cargo_description"])),
		Priority:         strings.TrimSpace(stringValue(payload["priority"])),
	}
	if content.JobReference == "" || content.PickupLocation == "" || content.DeliveryLocation == "" || content.CargoDescription == "" || content.Priority == "" {
		return jobInstructionFallbackContent{}, false
	}
	return content, true
}

func buildJobInstructionSMSBody(content jobInstructionFallbackContent) string {
	parts := []string{
		"Truckmoov job assignment.",
		fmt.Sprintf("Ref: %s.", content.JobReference),
		fmt.Sprintf("Pickup: %s.", content.PickupLocation),
		fmt.Sprintf("Delivery: %s.", content.DeliveryLocation),
		fmt.Sprintf("Cargo: %s.", content.CargoDescription),
		fmt.Sprintf("Priority: %s.", content.Priority),
	}
	if strings.TrimSpace(content.DriverName) != "" {
		parts = append([]string{"Truckmoov job assignment.", fmt.Sprintf("Driver %s.", content.DriverName)}, parts[1:]...)
	}
	return strings.Join(parts, " ")
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

type fixedClock struct {
	at time.Time
}

func (f fixedClock) Now() time.Time {
	return f.at
}

type driverStatusRequestContext struct {
	JobID     string
	DriverID  string
	Recipient string
}

func (u *HandleWhatsAppWebhookUseCase) recordDriverStatusRequestFromWebhook(ctx context.Context, conversation domain.WhatsAppConversation, message domain.WhatsAppConversationMessage, keyword, rawMessage, sourceContextMessageID string, receivedAt time.Time) (domain.WhatsAppConversationMessage, error) {
	payload := cloneAnyMap(message.Payload)
	payload["driver_status_keyword"] = keyword

	if u.driverStatusRequestSender == nil {
		payload["driver_status_request_recorded"] = false
		payload["driver_status_request_error"] = "driver status request sender is not configured"
		message.Payload = payload
		message.UpdatedAt = receivedAt.UTC()
		return u.repository.SaveMessage(ctx, message)
	}

	requestContext, ok, err := u.resolveDriverStatusRequestContext(ctx, conversation, sourceContextMessageID)
	if err != nil {
		payload["driver_status_request_recorded"] = false
		payload["driver_status_request_error"] = err.Error()
		message.Payload = payload
		message.UpdatedAt = receivedAt.UTC()
		return u.repository.SaveMessage(ctx, message)
	}
	if !ok {
		payload["driver_status_request_recorded"] = false
		payload["driver_status_request_error"] = "no job instruction context found for this reply"
		message.Payload = payload
		message.UpdatedAt = receivedAt.UTC()
		return u.repository.SaveMessage(ctx, message)
	}

	receipt, requestErr := u.driverStatusRequestSender.CreateDriverStatusRequest(ctx, CreateDriverStatusRequestFromWhatsAppCommand{
		TenantID:        conversation.TenantID,
		JobID:           requestContext.JobID,
		DriverID:        requestContext.DriverID,
		DriverPhone:     firstNonEmpty(requestContext.Recipient, message.FromPhone, conversation.DriverPhone),
		Keyword:         keyword,
		RawMessage:      rawMessage,
		SourceMessageID: message.ProviderMessageID,
		Channel:         "whatsapp",
	})
	if requestErr != nil {
		payload["driver_status_request_recorded"] = false
		payload["driver_status_request_error"] = requestErr.Error()
	} else {
		payload["driver_status_request_recorded"] = true
		payload["driver_status_request_id"] = receipt.RequestID
		payload["driver_status_requested"] = receipt.RequestedStatus
		payload["driver_status_request_job_id"] = requestContext.JobID
		payload["driver_status_request_driver_id"] = requestContext.DriverID
	}

	message.Payload = payload
	message.UpdatedAt = receivedAt.UTC()
	return u.repository.SaveMessage(ctx, message)
}

func (u *HandleWhatsAppWebhookUseCase) resolveDriverStatusRequestContext(ctx context.Context, conversation domain.WhatsAppConversation, sourceContextMessageID string) (driverStatusRequestContext, bool, error) {
	if strings.TrimSpace(sourceContextMessageID) != "" {
		originalMessage, ok, err := u.repository.FindMessageByProviderMessageID(ctx, strings.TrimSpace(sourceContextMessageID))
		if err != nil {
			return driverStatusRequestContext{}, false, err
		}
		if ok {
			if requestContext, contextOK := driverStatusRequestContextFromMessage(originalMessage, conversation); contextOK {
				return requestContext, true, nil
			}
		}
	}
	messages, err := u.repository.ListMessagesByConversation(ctx, conversation.TenantID, conversation.ID)
	if err != nil {
		return driverStatusRequestContext{}, false, err
	}
	for i := len(messages) - 1; i >= 0; i-- {
		candidate := messages[i]
		if requestContext, ok := driverStatusRequestContextFromMessage(candidate, conversation); ok {
			return requestContext, true, nil
		}
	}
	return driverStatusRequestContext{}, false, nil
}

func driverStatusRequestContextFromMessage(message domain.WhatsAppConversationMessage, conversation domain.WhatsAppConversation) (driverStatusRequestContext, bool) {
	if message.Direction != domain.WhatsAppDirectionOutbound {
		return driverStatusRequestContext{}, false
	}
	payload := cloneAnyMap(message.Payload)
	if strings.TrimSpace(stringValue(payload["kind"])) != "job_instruction" {
		return driverStatusRequestContext{}, false
	}
	jobID := strings.TrimSpace(stringValue(payload["job_id"]))
	driverID := strings.TrimSpace(stringValue(payload["driver_id"]))
	if jobID == "" || driverID == "" {
		return driverStatusRequestContext{}, false
	}
	return driverStatusRequestContext{
		JobID:     jobID,
		DriverID:  driverID,
		Recipient: firstNonEmpty(message.ToPhone, conversation.DriverPhone),
	}, true
}

func extractDriverStatusKeyword(message whatsAppWebhookMessage, preview string, payload map[string]any) (string, bool) {
	candidates := []string{
		strings.TrimSpace(preview),
		strings.TrimSpace(stringValue(payload["payload"])),
	}
	if message.Interactive != nil {
		if message.Interactive.ButtonReply != nil {
			candidates = append(candidates,
				strings.TrimSpace(message.Interactive.ButtonReply.ID),
				strings.TrimSpace(message.Interactive.ButtonReply.Title),
			)
		}
		if message.Interactive.ListReply != nil {
			candidates = append(candidates,
				strings.TrimSpace(message.Interactive.ListReply.ID),
				strings.TrimSpace(message.Interactive.ListReply.Title),
				strings.TrimSpace(message.Interactive.ListReply.Description),
			)
		}
	}

	for _, candidate := range candidates {
		switch normalizeDriverStatusKeyword(candidate) {
		case "ENROUTE":
			return "ENROUTE", true
		case "ARRIVED":
			return "ARRIVED", true
		case "LOADED":
			return "LOADED", true
		case "DELIVERED":
			return "DELIVERED", true
		}
	}
	return "", false
}

func messageContextID(message whatsAppWebhookMessage) string {
	if message.Context == nil {
		return ""
	}
	return strings.TrimSpace(message.Context.ID)
}

func normalizeDriverStatusKeyword(value string) string {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "", ".", "", ",", "", ":", "", ";", "", "/", "")
	return replacer.Replace(normalized)
}

type outboundConversationMessageParams struct {
	Conversation      domain.WhatsAppConversation
	TenantID          string
	Recipient         string
	DriverName        string
	MessageType       string
	Text              string
	TemplateName      string
	MediaType         string
	MediaID           string
	MediaLink         string
	Caption           string
	ProviderMessageID string
	Status            string
	Payload           map[string]any
	SentTemplate      bool
}

func resolveNotificationTenant(tenantID, defaultTenant string) (string, error) {
	resolved := strings.TrimSpace(tenantID)
	if resolved == "" {
		resolved = strings.TrimSpace(defaultTenant)
	}
	if resolved == "" {
		return "", ErrTenantIDRequired
	}
	return resolved, nil
}

func requireOpenConversation(ctx context.Context, repository WhatsAppConversationRepository, tenantID, recipient string, now time.Time) (domain.WhatsAppConversation, error) {
	conversation, ok, err := repository.FindConversationByTenantAndDriverPhone(ctx, tenantID, normalizeConversationPhone(recipient))
	if err != nil {
		return domain.WhatsAppConversation{}, err
	}
	if !ok {
		return domain.WhatsAppConversation{}, ErrWhatsAppConversationNotFound
	}
	if conversation.IsLocked(now) {
		return domain.WhatsAppConversation{}, ErrWhatsAppConversationLocked
	}
	return conversation, nil
}

func recordOutboundConversationMessage(ctx context.Context, repository WhatsAppConversationRepository, clock Clock, idGenerator IDGenerator, params outboundConversationMessageParams) (domain.WhatsAppConversation, domain.WhatsAppConversationMessage, error) {
	now := clock.Now().UTC()
	conversation := params.Conversation
	if strings.TrimSpace(conversation.ID) == "" {
		conversation = domain.WhatsAppConversation{
			ID:          idGenerator.New("wac"),
			TenantID:    strings.TrimSpace(params.TenantID),
			DriverPhone: normalizeConversationPhone(params.Recipient),
			CreatedAt:   now,
		}
	}
	if conversation.CreatedAt.IsZero() {
		conversation.CreatedAt = now
	}
	if strings.TrimSpace(params.DriverName) != "" {
		conversation.DriverName = strings.TrimSpace(params.DriverName)
	}
	conversation.TenantID = strings.TrimSpace(params.TenantID)
	conversation.DriverPhone = normalizeConversationPhone(params.Recipient)
	conversation.LastOutboundMessageAt = timePointer(now)
	conversation.LastMessageAt = timePointer(now)
	conversation.LastMessageDirection = domain.WhatsAppDirectionOutbound
	conversation.LastMessageType = strings.TrimSpace(params.MessageType)
	conversation.LastMessagePreview = buildConversationPreview(params.MessageType, params.Text, params.TemplateName, params.Caption)
	if params.SentTemplate {
		conversation.LastTemplateSentAt = timePointer(now)
	}
	conversation.UpdatedAt = now

	savedConversation, err := repository.SaveConversation(ctx, conversation)
	if err != nil {
		return domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, err
	}

	messageRecord, err := repository.SaveMessage(ctx, domain.WhatsAppConversationMessage{
		ID:                idGenerator.New("wam"),
		TenantID:          strings.TrimSpace(params.TenantID),
		ConversationID:    savedConversation.ID,
		ProviderMessageID: strings.TrimSpace(params.ProviderMessageID),
		Direction:         domain.WhatsAppDirectionOutbound,
		MessageType:       strings.TrimSpace(params.MessageType),
		Status:            strings.TrimSpace(params.Status),
		ToPhone:           normalizeConversationPhone(params.Recipient),
		Text:              strings.TrimSpace(params.Text),
		TemplateName:      strings.TrimSpace(params.TemplateName),
		MediaType:         strings.TrimSpace(params.MediaType),
		MediaID:           strings.TrimSpace(params.MediaID),
		MediaLink:         strings.TrimSpace(params.MediaLink),
		Caption:           strings.TrimSpace(params.Caption),
		Payload:           cloneAnyMap(params.Payload),
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	if err != nil {
		return domain.WhatsAppConversation{}, domain.WhatsAppConversationMessage{}, err
	}
	return savedConversation, messageRecord, nil
}

func conversationMatchesQuery(conversation domain.WhatsAppConversation, needle string) bool {
	if strings.Contains(strings.ToLower(conversation.DriverPhone), needle) {
		return true
	}
	if strings.Contains(strings.ToLower(conversation.DriverName), needle) {
		return true
	}
	return strings.Contains(strings.ToLower(conversation.LastMessagePreview), needle)
}

func buildMediaOutboxContent(mediaType, mediaID, mediaLink, caption string) string {
	payload, err := json.Marshal(map[string]any{
		"type":       strings.TrimSpace(mediaType),
		"media_id":   strings.TrimSpace(mediaID),
		"media_link": strings.TrimSpace(mediaLink),
		"caption":    strings.TrimSpace(caption),
	})
	if err != nil {
		return strings.TrimSpace(mediaType)
	}
	return string(payload)
}

func normalizeConversationPhone(recipient string) string {
	normalized := strings.TrimSpace(recipient)
	normalized = strings.TrimPrefix(normalized, "whatsapp:")
	replacer := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	normalized = replacer.Replace(normalized)
	return strings.TrimPrefix(normalized, "+")
}

func buildConversationPreview(messageType, text, templateName, caption string) string {
	trimmedText := strings.TrimSpace(text)
	trimmedCaption := strings.TrimSpace(caption)
	switch strings.ToLower(strings.TrimSpace(messageType)) {
	case "text", "button", "interactive":
		if trimmedText != "" {
			return truncatePreview(trimmedText)
		}
	case "template":
		if strings.TrimSpace(templateName) != "" {
			return truncatePreview("Template: " + strings.TrimSpace(templateName))
		}
		return "Template"
	case "image", "video", "audio", "document":
		prefix := "[" + strings.ToLower(strings.TrimSpace(messageType)) + "]"
		if trimmedCaption != "" {
			return truncatePreview(prefix + " " + trimmedCaption)
		}
		return prefix
	}
	if trimmedText != "" {
		return truncatePreview(trimmedText)
	}
	return truncatePreview(strings.TrimSpace(messageType))
}

func truncatePreview(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 140 {
		return trimmed
	}
	return trimmed[:137] + "..."
}

func cloneAnyMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func timePointer(value time.Time) *time.Time {
	utc := value.UTC()
	return &utc
}

func isSupportedOutboundMediaType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image", "video", "audio", "document":
		return true
	default:
		return false
	}
}

type whatsAppWebhookPayload struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Metadata struct {
					DisplayPhoneNumber string `json:"display_phone_number"`
					PhoneNumberID      string `json:"phone_number_id"`
				} `json:"metadata"`
				Contacts []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WAID string `json:"wa_id"`
				} `json:"contacts"`
				Messages []whatsAppWebhookMessage `json:"messages"`
				Statuses []whatsAppWebhookStatus  `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type whatsAppWebhookMessage struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Context   *struct {
		ID string `json:"id"`
	} `json:"context"`
	Text *struct {
		Body string `json:"body"`
	} `json:"text"`
	Button *struct {
		Text    string `json:"text"`
		Payload string `json:"payload"`
	} `json:"button"`
	Interactive *struct {
		Type        string `json:"type"`
		ButtonReply *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"button_reply"`
		ListReply *struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"list_reply"`
	} `json:"interactive"`
	Image *struct {
		ID       string `json:"id"`
		Caption  string `json:"caption"`
		MimeType string `json:"mime_type"`
	} `json:"image"`
	Video *struct {
		ID       string `json:"id"`
		Caption  string `json:"caption"`
		MimeType string `json:"mime_type"`
	} `json:"video"`
	Audio *struct {
		ID       string `json:"id"`
		MimeType string `json:"mime_type"`
	} `json:"audio"`
	Document *struct {
		ID       string `json:"id"`
		Caption  string `json:"caption"`
		Filename string `json:"filename"`
		MimeType string `json:"mime_type"`
	} `json:"document"`
}

type whatsAppWebhookStatus struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	RecipientID  string `json:"recipient_id"`
	Conversation *struct {
		ID     string `json:"id"`
		Origin *struct {
			Type string `json:"type"`
		} `json:"origin"`
	} `json:"conversation"`
	Pricing map[string]any   `json:"pricing"`
	Errors  []map[string]any `json:"errors"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func resolveWebhookConversation(ctx context.Context, repository WhatsAppConversationRepository, message whatsAppWebhookMessage) (domain.WhatsAppConversation, bool, error) {
	if message.Context != nil && strings.TrimSpace(message.Context.ID) != "" {
		originalMessage, ok, err := repository.FindMessageByProviderMessageID(ctx, strings.TrimSpace(message.Context.ID))
		if err != nil {
			return domain.WhatsAppConversation{}, false, err
		}
		if ok {
			conversation, ok, err := repository.FindConversationByTenantAndID(ctx, originalMessage.TenantID, originalMessage.ConversationID)
			return conversation, ok, err
		}
	}
	return repository.FindMostRecentConversationByDriverPhone(ctx, normalizeConversationPhone(message.From))
}

func extractWebhookMessageDetails(message whatsAppWebhookMessage) (preview, messageType, mediaType, mediaID, mediaLink, caption string, payload map[string]any) {
	messageType = strings.ToLower(strings.TrimSpace(message.Type))
	payload = map[string]any{
		"type": messageType,
	}
	switch messageType {
	case "text":
		if message.Text != nil {
			preview = strings.TrimSpace(message.Text.Body)
			payload["body"] = preview
		}
	case "button":
		if message.Button != nil {
			preview = firstNonEmpty(message.Button.Text, message.Button.Payload)
			payload["text"] = strings.TrimSpace(message.Button.Text)
			payload["payload"] = strings.TrimSpace(message.Button.Payload)
		}
	case "interactive":
		if message.Interactive != nil {
			if message.Interactive.ButtonReply != nil {
				preview = firstNonEmpty(message.Interactive.ButtonReply.Title, message.Interactive.ButtonReply.ID)
				payload["button_reply"] = map[string]any{"id": strings.TrimSpace(message.Interactive.ButtonReply.ID), "title": strings.TrimSpace(message.Interactive.ButtonReply.Title)}
			} else if message.Interactive.ListReply != nil {
				preview = firstNonEmpty(message.Interactive.ListReply.Title, message.Interactive.ListReply.Description, message.Interactive.ListReply.ID)
				payload["list_reply"] = map[string]any{"id": strings.TrimSpace(message.Interactive.ListReply.ID), "title": strings.TrimSpace(message.Interactive.ListReply.Title), "description": strings.TrimSpace(message.Interactive.ListReply.Description)}
			}
		}
	case "image":
		mediaType = "image"
		if message.Image != nil {
			mediaID = strings.TrimSpace(message.Image.ID)
			caption = strings.TrimSpace(message.Image.Caption)
			preview = buildConversationPreview("image", "", "", caption)
			payload["media_id"] = mediaID
			payload["caption"] = caption
			payload["mime_type"] = strings.TrimSpace(message.Image.MimeType)
		}
	case "video":
		mediaType = "video"
		if message.Video != nil {
			mediaID = strings.TrimSpace(message.Video.ID)
			caption = strings.TrimSpace(message.Video.Caption)
			preview = buildConversationPreview("video", "", "", caption)
			payload["media_id"] = mediaID
			payload["caption"] = caption
			payload["mime_type"] = strings.TrimSpace(message.Video.MimeType)
		}
	case "audio":
		mediaType = "audio"
		if message.Audio != nil {
			mediaID = strings.TrimSpace(message.Audio.ID)
			preview = buildConversationPreview("audio", "", "", "")
			payload["media_id"] = mediaID
			payload["mime_type"] = strings.TrimSpace(message.Audio.MimeType)
		}
	case "document":
		mediaType = "document"
		if message.Document != nil {
			mediaID = strings.TrimSpace(message.Document.ID)
			caption = strings.TrimSpace(message.Document.Caption)
			preview = buildConversationPreview("document", "", "", caption)
			payload["media_id"] = mediaID
			payload["caption"] = caption
			payload["filename"] = strings.TrimSpace(message.Document.Filename)
			payload["mime_type"] = strings.TrimSpace(message.Document.MimeType)
		}
	default:
		preview = truncatePreview(messageType)
	}
	if strings.TrimSpace(preview) == "" {
		preview = truncatePreview(messageType)
	}
	return preview, messageType, mediaType, mediaID, mediaLink, caption, payload
}

func parseWebhookTimestamp(raw string, fallback time.Time) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback.UTC()
	}
	seconds, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return fallback.UTC()
	}
	return time.Unix(seconds, 0).UTC()
}

func normalizeWebhookDeliveryStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "delivered", "read":
		return domain.StatusDelivered
	case "failed", "undelivered":
		return domain.StatusFailed
	case "sent":
		return domain.StatusSent
	default:
		trimmed := strings.ToLower(strings.TrimSpace(raw))
		if trimmed == "" {
			return domain.StatusSent
		}
		return trimmed
	}
}

func buildWebhookStatusPayload(status whatsAppWebhookStatus) map[string]any {
	payload := map[string]any{
		"delivery_status": normalizeWebhookDeliveryStatus(status.Status),
		"recipient_id":    strings.TrimSpace(status.RecipientID),
	}
	if strings.TrimSpace(status.Status) != "" {
		payload["provider_status"] = strings.TrimSpace(status.Status)
	}
	if status.Conversation != nil {
		payload["conversation"] = map[string]any{
			"id": strings.TrimSpace(status.Conversation.ID),
		}
		if status.Conversation.Origin != nil && strings.TrimSpace(status.Conversation.Origin.Type) != "" {
			payload["conversation"].(map[string]any)["origin"] = map[string]any{
				"type": strings.TrimSpace(status.Conversation.Origin.Type),
			}
		}
	}
	if len(status.Pricing) > 0 {
		payload["pricing"] = cloneAnyMap(status.Pricing)
	}
	if len(status.Errors) > 0 {
		payload["errors"] = cloneAnySliceOfMaps(status.Errors)
	}
	return payload
}

func mergeAnyMap(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return map[string]any{}
	}
	merged := cloneAnyMap(base)
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

func cloneAnySliceOfMaps(values []map[string]any) []map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]map[string]any, 0, len(values))
	for _, item := range values {
		cloned = append(cloned, cloneAnyMap(item))
	}
	return cloned
}

func webhookStatusErrorText(status whatsAppWebhookStatus) string {
	parts := make([]string, 0, len(status.Errors))
	for _, item := range status.Errors {
		if message := strings.TrimSpace(stringValue(item["message"])); message != "" {
			parts = append(parts, message)
			continue
		}
		if title := strings.TrimSpace(stringValue(item["title"])); title != "" {
			parts = append(parts, title)
		}
	}
	return strings.Join(parts, "; ")
}

func publishWebhookDeliveryEvent(ctx context.Context, eventPublisher DeliveryEventPublisher, idGenerator IDGenerator, occurredAt time.Time, message domain.OutboxMessage, errorText string) error {
	event, err := domain.NewMessageDeliveryUpdated(domain.MessageDeliveryUpdated{
		EventID:    idGenerator.New("evt"),
		OccurredAt: occurredAt,
		TenantID:   message.TenantID,
		MessageID:  message.ID,
		Channel:    message.Channel,
		Status:     message.Status,
		ErrorText:  strings.TrimSpace(errorText),
	})
	if err != nil {
		return err
	}
	return eventPublisher.PublishDeliveryUpdated(ctx, event)
}
