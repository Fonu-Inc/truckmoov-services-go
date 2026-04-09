package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type JobInstructionHeaderMedia struct {
	Type     string
	Link     string
	ID       string
	Filename string
}

type JobInstructionButtonParameter struct {
	Index   int
	Type    string
	Text    string
	Payload string
}

type SendJobInstructionCommand struct {
	TenantID         string
	JobID            string
	DriverID         string
	Recipient        string
	TemplateName     string
	Language         string
	DriverName       string
	JobReference     string
	PickupLocation   string
	DeliveryLocation string
	CargoDescription string
	Priority         string
	HeaderMedia      *JobInstructionHeaderMedia
	ButtonParameters []JobInstructionButtonParameter
}

type SendJobInstructionUseCase struct {
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
	ErrJobInstructionRecipientRequired          = errors.New("recipient is required")
	ErrJobInstructionTemplateNameRequired       = errors.New("template name is required")
	ErrJobInstructionTemplateNotFound           = errors.New("job assignment template not found")
	ErrJobInstructionTemplateNotApproved        = errors.New("job assignment template must be approved before sending")
	ErrJobInstructionTemplateNotCompatible      = errors.New("template is not a valid job assignment template")
	ErrJobInstructionDriverNameRequired         = errors.New("driver name is required")
	ErrJobInstructionReferenceRequired          = errors.New("job reference is required")
	ErrJobInstructionPickupLocationRequired     = errors.New("pickup location is required")
	ErrJobInstructionDeliveryLocationRequired   = errors.New("delivery location is required")
	ErrJobInstructionCargoDescriptionRequired   = errors.New("cargo description is required")
	ErrJobInstructionPriorityRequired           = errors.New("priority is required")
	ErrJobInstructionHeaderMediaRequired        = errors.New("header media is required for this template")
	ErrJobInstructionHeaderMediaTypeMismatch    = errors.New("header media type does not match the template header format")
	ErrJobInstructionHeaderMediaValueRequired   = errors.New("header media requires either a link or id")
	ErrJobInstructionButtonParameterRequired    = errors.New("button parameter is required for this template")
	ErrJobInstructionButtonParameterValueNeeded = errors.New("button parameter value is required for this template")
)

func NewSendJobInstructionUseCase(repository OutboxRepository, conversationRepository WhatsAppConversationRepository, templateProvider WhatsAppTemplateProvider, messageProvider WhatsAppTemplateMessageProvider, eventPublisher DeliveryEventPublisher, clock Clock, idGenerator IDGenerator, defaultTenant string) *SendJobInstructionUseCase {
	return &SendJobInstructionUseCase{repository: repository, conversationRepository: conversationRepository, templateProvider: templateProvider, messageProvider: messageProvider, eventPublisher: eventPublisher, clock: clock, idGenerator: idGenerator, defaultTenant: defaultTenant}
}

func (u *SendJobInstructionUseCase) Execute(ctx context.Context, command SendJobInstructionCommand) (domain.OutboxMessage, error) {
	tenantID, err := resolveNotificationTenant(command.TenantID, u.defaultTenant)
	if err != nil {
		return domain.OutboxMessage{}, err
	}
	if strings.TrimSpace(command.Recipient) == "" {
		return domain.OutboxMessage{}, ErrJobInstructionRecipientRequired
	}
	if strings.TrimSpace(command.TemplateName) == "" {
		return domain.OutboxMessage{}, ErrJobInstructionTemplateNameRequired
	}

	template, err := u.findTemplateByName(ctx, tenantID, strings.TrimSpace(command.TemplateName))
	if err != nil {
		return domain.OutboxMessage{}, err
	}
	if strings.EqualFold(strings.TrimSpace(template.Status), "APPROVED") == false {
		return domain.OutboxMessage{}, ErrJobInstructionTemplateNotApproved
	}
	if hasMinimumJobAssignmentPlaceholders(template.Components) == false {
		return domain.OutboxMessage{}, ErrJobInstructionTemplateNotCompatible
	}

	sendComponents, err := buildJobInstructionSendComponents(template, command)
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
	content := buildJobInstructionContent(command, template.Name, language)
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
		TenantID:   tenantID,
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
		DriverName:        command.DriverName,
		MessageType:       "template",
		Text:              buildConversationPreview("template", "", template.Name, ""),
		TemplateName:      template.Name,
		ProviderMessageID: result.MessageID,
		Status:            result.Status,
		Payload: map[string]any{
			"kind":              "job_instruction",
			"type":              "template",
			"job_id":            strings.TrimSpace(command.JobID),
			"driver_id":         strings.TrimSpace(command.DriverID),
			"template_name":     template.Name,
			"language":          language,
			"driver_name":       strings.TrimSpace(command.DriverName),
			"job_reference":     strings.TrimSpace(command.JobReference),
			"pickup_location":   strings.TrimSpace(command.PickupLocation),
			"delivery_location": strings.TrimSpace(command.DeliveryLocation),
			"cargo_description": strings.TrimSpace(command.CargoDescription),
			"priority":          strings.TrimSpace(command.Priority),
		},
		SentTemplate: true,
	})
	if err != nil {
		return domain.OutboxMessage{}, err
	}

	return message, nil
}

func (u *SendJobInstructionUseCase) findTemplateByName(ctx context.Context, tenantID, templateName string) (domain.WhatsAppTemplate, error) {
	return findWhatsAppTemplateByName(ctx, u.templateProvider, tenantID, templateName)
}

func findWhatsAppTemplateByName(ctx context.Context, templateProvider WhatsAppTemplateProvider, tenantID, templateName string) (domain.WhatsAppTemplate, error) {
	after := ""
	for page := 0; page < 20; page++ {
		response, err := templateProvider.ListTemplates(ctx, domain.WhatsAppTemplateListRequest{TenantID: strings.TrimSpace(tenantID), Limit: 100, After: after})
		if err != nil {
			return domain.WhatsAppTemplate{}, err
		}
		for _, template := range response.Data {
			if strings.EqualFold(strings.TrimSpace(template.Name), templateName) {
				return template, nil
			}
		}
		if response.Paging == nil {
			break
		}
		nextAfter := strings.TrimSpace(response.Paging.Cursors["after"])
		if nextAfter == "" || nextAfter == after {
			break
		}
		after = nextAfter
	}
	return domain.WhatsAppTemplate{}, ErrJobInstructionTemplateNotFound
}

func buildJobInstructionSendComponents(template domain.WhatsAppTemplate, command SendJobInstructionCommand) ([]map[string]any, error) {
	components := make([]map[string]any, 0, len(template.Components))
	for _, rawComponent := range template.Components {
		componentType := strings.ToUpper(strings.TrimSpace(stringValue(rawComponent["type"])))
		switch componentType {
		case "HEADER":
			component, err := buildJobInstructionHeaderComponent(rawComponent, command)
			if err != nil {
				return nil, err
			}
			if component != nil {
				components = append(components, component)
			}
		case "BODY":
			component, err := buildJobInstructionTextComponent("body", stringValue(rawComponent["text"]), command)
			if err != nil {
				return nil, err
			}
			if component != nil {
				components = append(components, component)
			}
		case "BUTTONS":
			buttonComponents, err := buildJobInstructionButtonComponents(rawComponent, command)
			if err != nil {
				return nil, err
			}
			components = append(components, buttonComponents...)
		}
	}
	return components, nil
}

func buildJobInstructionHeaderComponent(component map[string]any, command SendJobInstructionCommand) (map[string]any, error) {
	format := strings.ToUpper(strings.TrimSpace(stringValue(component["format"])))
	switch format {
	case "", "TEXT":
		return buildJobInstructionTextComponent("header", stringValue(component["text"]), command)
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

func buildJobInstructionTextComponent(componentType, text string, command SendJobInstructionCommand) (map[string]any, error) {
	indices := extractPlaceholderIndices(text)
	if len(indices) == 0 {
		return nil, nil
	}
	parameters := make([]map[string]any, 0, len(indices))
	for _, index := range indices {
		value, err := jobInstructionVariableValue(index, command)
		if err != nil {
			return nil, err
		}
		parameters = append(parameters, map[string]any{
			"type": "text",
			"text": value,
		})
	}
	return map[string]any{
		"type":       componentType,
		"parameters": parameters,
	}, nil
}

func buildJobInstructionButtonComponents(component map[string]any, command SendJobInstructionCommand) ([]map[string]any, error) {
	buttons := normalizeTemplateButtons(component["buttons"])
	result := make([]map[string]any, 0, len(buttons))
	for index, button := range buttons {
		buttonType := strings.ToUpper(strings.TrimSpace(stringValue(button["type"])))
		buttonText := stringValue(button["url"])
		if buttonType == "URL" && len(extractPlaceholderIndices(buttonText)) > 0 {
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
		}
	}
	return result, nil
}

func extractPlaceholderIndices(text string) []int {
	matches := whatsAppTemplatePlaceholderPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[int]struct{}{}
	indices := make([]int, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		index, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		indices = append(indices, index)
	}
	sort.Ints(indices)
	return indices
}

func jobInstructionVariableValue(index int, command SendJobInstructionCommand) (string, error) {
	switch index {
	case 1:
		if strings.TrimSpace(command.DriverName) == "" {
			return "", ErrJobInstructionDriverNameRequired
		}
		return strings.TrimSpace(command.DriverName), nil
	case 2:
		if strings.TrimSpace(command.JobReference) == "" {
			return "", ErrJobInstructionReferenceRequired
		}
		return strings.TrimSpace(command.JobReference), nil
	case 3:
		if strings.TrimSpace(command.PickupLocation) == "" {
			return "", ErrJobInstructionPickupLocationRequired
		}
		return strings.TrimSpace(command.PickupLocation), nil
	case 4:
		if strings.TrimSpace(command.DeliveryLocation) == "" {
			return "", ErrJobInstructionDeliveryLocationRequired
		}
		return strings.TrimSpace(command.DeliveryLocation), nil
	case 5:
		if strings.TrimSpace(command.CargoDescription) == "" {
			return "", ErrJobInstructionCargoDescriptionRequired
		}
		return strings.TrimSpace(command.CargoDescription), nil
	case 6:
		if strings.TrimSpace(command.Priority) == "" {
			return "", ErrJobInstructionPriorityRequired
		}
		return strings.TrimSpace(command.Priority), nil
	default:
		return "", fmt.Errorf("unsupported job instruction placeholder {{%d}}", index)
	}
}

func normalizeTemplateButtons(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			button, ok := item.(map[string]any)
			if ok {
				result = append(result, button)
			}
		}
		return result
	default:
		return nil
	}
}

func findJobInstructionButtonParameter(parameters []JobInstructionButtonParameter, index int) (JobInstructionButtonParameter, bool) {
	for _, parameter := range parameters {
		if parameter.Index == index {
			return parameter, true
		}
	}
	return JobInstructionButtonParameter{}, false
}

func stringValue(value any) string {
	stringValue, ok := value.(string)
	if ok {
		return stringValue
	}
	return ""
}

func buildJobInstructionContent(command SendJobInstructionCommand, templateName, language string) string {
	payload, err := json.Marshal(map[string]any{
		"kind":              "job_instruction",
		"template_name":     templateName,
		"language":          language,
		"driver_name":       strings.TrimSpace(command.DriverName),
		"job_reference":     strings.TrimSpace(command.JobReference),
		"pickup_location":   strings.TrimSpace(command.PickupLocation),
		"delivery_location": strings.TrimSpace(command.DeliveryLocation),
		"cargo_description": strings.TrimSpace(command.CargoDescription),
		"priority":          strings.TrimSpace(command.Priority),
	})
	if err != nil {
		return "job_instruction"
	}
	return string(payload)
}

func publishNotificationDeliveryEvent(ctx context.Context, eventPublisher DeliveryEventPublisher, idGenerator IDGenerator, clock Clock, message domain.OutboxMessage, sendErr error) error {
	event, err := domain.NewMessageDeliveryUpdated(domain.MessageDeliveryUpdated{
		EventID:    idGenerator.New("evt"),
		OccurredAt: clock.Now(),
		TenantID:   message.TenantID,
		MessageID:  message.ID,
		Channel:    message.Channel,
		Status:     message.Status,
	})
	if err != nil {
		return err
	}
	if sendErr != nil {
		event.ErrorText = sendErr.Error()
	}
	return eventPublisher.PublishDeliveryUpdated(ctx, event)
}
