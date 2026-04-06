package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type SendOtherTemplateCommand struct {
	TenantID         string
	Recipient        string
	TemplateName     string
	Language         string
	Variables        map[string]string
	HeaderMedia      *JobInstructionHeaderMedia
	ButtonParameters []JobInstructionButtonParameter
}

type SendOtherTemplateUseCase struct {
	repository             OutboxRepository
	conversationRepository WhatsAppConversationRepository
	templateProvider       WhatsAppTemplateProvider
	messageProvider        WhatsAppTemplateMessageProvider
	eventPublisher         DeliveryEventPublisher
	clock                  Clock
	idGenerator            IDGenerator
	defaultTenant          string
}

var (
	ErrOtherTemplateRecipientRequired       = errors.New("recipient is required")
	ErrOtherTemplateTemplateNameRequired    = errors.New("template name is required")
	ErrOtherTemplateTemplateNotFound        = errors.New("template not found")
	ErrOtherTemplateTemplateNotApproved     = errors.New("template must be approved before sending")
	ErrOtherTemplateJobAssignmentNotAllowed = errors.New("job assignment templates must be sent with the job instruction endpoint")
	ErrOtherTemplateVariableRequired        = errors.New("all configured template variables must be provided")
)

func NewSendOtherTemplateUseCase(repository OutboxRepository, conversationRepository WhatsAppConversationRepository, templateProvider WhatsAppTemplateProvider, messageProvider WhatsAppTemplateMessageProvider, eventPublisher DeliveryEventPublisher, clock Clock, idGenerator IDGenerator, defaultTenant string) *SendOtherTemplateUseCase {
	return &SendOtherTemplateUseCase{repository: repository, conversationRepository: conversationRepository, templateProvider: templateProvider, messageProvider: messageProvider, eventPublisher: eventPublisher, clock: clock, idGenerator: idGenerator, defaultTenant: defaultTenant}
}

func (u *SendOtherTemplateUseCase) Execute(ctx context.Context, command SendOtherTemplateCommand) (domain.OutboxMessage, error) {
	tenantID, err := resolveNotificationTenant(command.TenantID, u.defaultTenant)
	if err != nil {
		return domain.OutboxMessage{}, err
	}
	if strings.TrimSpace(command.Recipient) == "" {
		return domain.OutboxMessage{}, ErrOtherTemplateRecipientRequired
	}
	if strings.TrimSpace(command.TemplateName) == "" {
		return domain.OutboxMessage{}, ErrOtherTemplateTemplateNameRequired
	}

	template, err := findWhatsAppTemplateByName(ctx, u.templateProvider, strings.TrimSpace(command.TemplateName))
	if err != nil {
		if errors.Is(err, ErrJobInstructionTemplateNotFound) {
			return domain.OutboxMessage{}, ErrOtherTemplateTemplateNotFound
		}
		return domain.OutboxMessage{}, err
	}
	if strings.EqualFold(strings.TrimSpace(template.Status), "APPROVED") == false {
		return domain.OutboxMessage{}, ErrOtherTemplateTemplateNotApproved
	}
	if hasMinimumJobAssignmentPlaceholders(template.Components) {
		return domain.OutboxMessage{}, ErrOtherTemplateJobAssignmentNotAllowed
	}

	sendComponents, err := buildOtherTemplateSendComponents(template, command)
	if err != nil {
		return domain.OutboxMessage{}, err
	}

	language := strings.TrimSpace(command.Language)
	if language == "" {
		language = strings.TrimSpace(template.Language)
	}
	if language == "" {
		language = "en_US"
	}

	now := u.clock.Now()
	content := buildOtherTemplateContent(command, template.Name, language)
	message, err := domain.NewOutboxMessage(domain.OutboxMessage{
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
		return domain.OutboxMessage{}, fmt.Errorf("build outbox message: %w", err)
	}

	if err := u.repository.Save(message); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("save queued outbox message: %w", err)
	}

	result, sendErr := u.messageProvider.SendTemplate(ctx, domain.WhatsAppTemplateMessageRequest{
		Recipient:  strings.TrimSpace(command.Recipient),
		Name:       template.Name,
		Language:   language,
		Components: sendComponents,
	})
	if sendErr != nil {
		message = message.MarkFailed(u.clock.Now())
	} else {
		message = message.MarkSent(result.Status, result.MessageID, u.clock.Now())
	}

	if err := u.repository.Save(message); err != nil {
		return domain.OutboxMessage{}, fmt.Errorf("save delivered outbox message: %w", err)
	}

	if publishErr := publishNotificationDeliveryEvent(ctx, u.eventPublisher, u.idGenerator, u.clock, message, sendErr); publishErr != nil {
		// best effort
	}
	if sendErr != nil {
		return message, sendErr
	}

	_, _, err = recordOutboundConversationMessage(ctx, u.conversationRepository, u.clock, u.idGenerator, outboundConversationMessageParams{
		TenantID:          tenantID,
		Recipient:         command.Recipient,
		MessageType:       "template",
		Text:              buildConversationPreview("template", "", template.Name, ""),
		TemplateName:      template.Name,
		ProviderMessageID: result.MessageID,
		Status:            result.Status,
		Payload: map[string]any{
			"type":          "template",
			"template_name": template.Name,
			"language":      language,
			"variables":     command.Variables,
		},
		SentTemplate: true,
	})
	if err != nil {
		return domain.OutboxMessage{}, err
	}

	return message, nil
}

func buildOtherTemplateSendComponents(template domain.WhatsAppTemplate, command SendOtherTemplateCommand) ([]map[string]any, error) {
	components := make([]map[string]any, 0, len(template.Components))
	variables := normalizeTemplateVariables(command.Variables)
	for _, rawComponent := range template.Components {
		componentType := strings.ToUpper(strings.TrimSpace(stringValue(rawComponent["type"])))
		switch componentType {
		case "HEADER":
			component, err := buildOtherTemplateHeaderComponent(rawComponent, variables, command)
			if err != nil {
				return nil, err
			}
			if component != nil {
				components = append(components, component)
			}
		case "BODY":
			component, err := buildOtherTemplateTextComponent("body", stringValue(rawComponent["text"]), variables)
			if err != nil {
				return nil, err
			}
			if component != nil {
				components = append(components, component)
			}
		case "BUTTONS":
			buttonComponents, err := buildOtherTemplateButtonComponents(rawComponent, command)
			if err != nil {
				return nil, err
			}
			components = append(components, buttonComponents...)
		}
	}
	return components, nil
}

func buildOtherTemplateHeaderComponent(component map[string]any, variables map[int]string, command SendOtherTemplateCommand) (map[string]any, error) {
	format := strings.ToUpper(strings.TrimSpace(stringValue(component["format"])))
	switch format {
	case "", "TEXT":
		return buildOtherTemplateTextComponent("header", stringValue(component["text"]), variables)
	case "IMAGE", "VIDEO", "DOCUMENT":
		if command.HeaderMedia == nil {
			return nil, ErrJobInstructionHeaderMediaRequired
		}
		if strings.EqualFold(strings.TrimSpace(command.HeaderMedia.Type), strings.ToLower(format)) == false && strings.EqualFold(strings.TrimSpace(command.HeaderMedia.Type), format) == false {
			return nil, ErrJobInstructionHeaderMediaTypeMismatch
		}
		mediaPayload := map[string]any{}
		if strings.TrimSpace(command.HeaderMedia.Link) != "" {
			mediaPayload["link"] = strings.TrimSpace(command.HeaderMedia.Link)
		} else if strings.TrimSpace(command.HeaderMedia.ID) != "" {
			mediaPayload["id"] = strings.TrimSpace(command.HeaderMedia.ID)
		} else {
			return nil, ErrJobInstructionHeaderMediaValueRequired
		}
		if format == "DOCUMENT" && strings.TrimSpace(command.HeaderMedia.Filename) != "" {
			mediaPayload["filename"] = strings.TrimSpace(command.HeaderMedia.Filename)
		}
		parameterType := strings.ToLower(format)
		return map[string]any{
			"type": "header",
			"parameters": []map[string]any{{
				"type":        parameterType,
				parameterType: mediaPayload,
			}},
		}, nil
	default:
		return nil, nil
	}
}

func buildOtherTemplateTextComponent(componentType, text string, variables map[int]string) (map[string]any, error) {
	indices := extractPlaceholderIndices(text)
	if len(indices) == 0 {
		return nil, nil
	}
	parameters := make([]map[string]any, 0, len(indices))
	for _, index := range indices {
		value, ok := variables[index]
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%w: template variable {{%d}}", ErrOtherTemplateVariableRequired, index)
		}
		parameters = append(parameters, map[string]any{
			"type": "text",
			"text": strings.TrimSpace(value),
		})
	}
	return map[string]any{
		"type":       componentType,
		"parameters": parameters,
	}, nil
}

func buildOtherTemplateButtonComponents(component map[string]any, command SendOtherTemplateCommand) ([]map[string]any, error) {
	buttons := normalizeTemplateButtons(component["buttons"])
	result := make([]map[string]any, 0, len(buttons))
	for index, button := range buttons {
		buttonType := strings.ToUpper(strings.TrimSpace(stringValue(button["type"])))
		switch buttonType {
		case "URL":
			if len(extractPlaceholderIndices(stringValue(button["url"]))) == 0 {
				continue
			}
			parameter, ok := findJobInstructionButtonParameter(command.ButtonParameters, index)
			if ok == false {
				return nil, ErrJobInstructionButtonParameterRequired
			}
			if strings.TrimSpace(parameter.Text) == "" {
				return nil, ErrJobInstructionButtonParameterValueNeeded
			}
			result = append(result, map[string]any{
				"type":     "button",
				"sub_type": "url",
				"index":    strconv.Itoa(index),
				"parameters": []map[string]any{{
					"type": "text",
					"text": strings.TrimSpace(parameter.Text),
				}},
			})
		case "COPY_CODE":
			parameter, ok := findJobInstructionButtonParameter(command.ButtonParameters, index)
			if ok == false {
				return nil, ErrJobInstructionButtonParameterRequired
			}
			value := strings.TrimSpace(parameter.Payload)
			if value == "" {
				value = strings.TrimSpace(parameter.Text)
			}
			if value == "" {
				return nil, ErrJobInstructionButtonParameterValueNeeded
			}
			result = append(result, map[string]any{
				"type":     "button",
				"sub_type": "copy_code",
				"index":    strconv.Itoa(index),
				"parameters": []map[string]any{{
					"type":        "coupon_code",
					"coupon_code": value,
				}},
			})
		}
	}
	return result, nil
}

func normalizeTemplateVariables(values map[string]string) map[int]string {
	if len(values) == 0 {
		return nil
	}
	normalized := make(map[int]string, len(values))
	for rawKey, rawValue := range values {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			continue
		}
		matches := whatsAppTemplatePlaceholderPattern.FindStringSubmatch(key)
		if len(matches) >= 2 {
			key = matches[1]
		}
		index, err := strconv.Atoi(key)
		if err != nil || index <= 0 {
			continue
		}
		normalized[index] = strings.TrimSpace(rawValue)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func buildOtherTemplateContent(command SendOtherTemplateCommand, templateName, language string) string {
	payload, err := json.Marshal(map[string]any{
		"template_name": templateName,
		"language":      language,
		"variables":     command.Variables,
	})
	if err != nil {
		return "other_template"
	}
	return string(payload)
}
