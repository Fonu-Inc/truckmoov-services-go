package application

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const (
	WhatsAppTemplateTypeJobAssignment = "job_assignment"
	WhatsAppTemplateTypeOther         = "other"

	jobAssignmentMinimumVariableCount = 6
)

var (
	ErrWhatsAppTemplateNameRequired                   = errors.New("whatsapp template name is required")
	ErrWhatsAppTemplateBodyOrComponentsRequired       = errors.New("whatsapp template body or components are required")
	ErrWhatsAppTemplateUnsupportedType                = errors.New("template type must be one of: job_assignment, other")
	ErrWhatsAppTemplateJobAssignmentVariablesRequired = errors.New("job assignment templates must include placeholders {{1}} to {{6}} across their components")
	ErrTenantIDRequired                               = errors.New("tenant id is required")

	whatsAppTemplatePlaceholderPattern = regexp.MustCompile("\\{\\{\\s*(\\d+)\\s*\\}\\}")
)

type CreateWhatsAppTemplateCommand struct {
	TenantID            string
	TemplateType        string
	Name                string
	Header              string
	Body                string
	Language            string
	Category            string
	AllowCategoryChange *bool
	ParameterFormat     string
	Components          []map[string]any
}

type CreateWhatsAppTemplateUseCase struct {
	provider WhatsAppTemplateProvider
}

func NewCreateWhatsAppTemplateUseCase(provider WhatsAppTemplateProvider) *CreateWhatsAppTemplateUseCase {
	return &CreateWhatsAppTemplateUseCase{provider: provider}
}

func (u *CreateWhatsAppTemplateUseCase) Execute(ctx context.Context, command CreateWhatsAppTemplateCommand) (domain.WhatsAppTemplateCreateResponse, error) {
	if strings.TrimSpace(command.TenantID) == "" {
		return domain.WhatsAppTemplateCreateResponse{}, ErrTenantIDRequired
	}
	if strings.TrimSpace(command.Name) == "" {
		return domain.WhatsAppTemplateCreateResponse{}, ErrWhatsAppTemplateNameRequired
	}

	templateType, err := normalizeWhatsAppTemplateType(command.TemplateType)
	if err != nil {
		return domain.WhatsAppTemplateCreateResponse{}, err
	}

	components := normalizeWhatsAppTemplateComponents(command.Components)
	if len(components) == 0 {
		components = buildWhatsAppTemplateComponents(command.Header, command.Body)
	}
	if len(components) == 0 {
		return domain.WhatsAppTemplateCreateResponse{}, ErrWhatsAppTemplateBodyOrComponentsRequired
	}

	if templateType == WhatsAppTemplateTypeJobAssignment && !hasMinimumJobAssignmentPlaceholders(components) {
		return domain.WhatsAppTemplateCreateResponse{}, ErrWhatsAppTemplateJobAssignmentVariablesRequired
	}

	allowCategoryChange := command.AllowCategoryChange
	if allowCategoryChange == nil {
		defaultAllowCategoryChange := true
		allowCategoryChange = &defaultAllowCategoryChange
	}

	language := strings.TrimSpace(command.Language)
	if language == "" {
		language = "en_US"
	}

	category := strings.TrimSpace(command.Category)
	if category == "" {
		category = "UTILITY"
	}

	parameterFormat := strings.TrimSpace(command.ParameterFormat)
	if parameterFormat == "" {
		parameterFormat = "POSITIONAL"
	}

	return u.provider.CreateTemplate(ctx, domain.WhatsAppTemplateCreateRequest{
		TenantID:            strings.TrimSpace(command.TenantID),
		Name:                strings.TrimSpace(command.Name),
		Language:            language,
		Category:            category,
		AllowCategoryChange: allowCategoryChange,
		ParameterFormat:     parameterFormat,
		Components:          components,
	})
}

type ListWhatsAppTemplatesQuery struct {
	TenantID string
	Limit    int
	After    string
}

type ListWhatsAppTemplatesUseCase struct {
	provider WhatsAppTemplateProvider
}

func NewListWhatsAppTemplatesUseCase(provider WhatsAppTemplateProvider) *ListWhatsAppTemplatesUseCase {
	return &ListWhatsAppTemplatesUseCase{provider: provider}
}

func (u *ListWhatsAppTemplatesUseCase) Execute(ctx context.Context, query ListWhatsAppTemplatesQuery) (domain.WhatsAppTemplateListResponse, error) {
	if strings.TrimSpace(query.TenantID) == "" {
		return domain.WhatsAppTemplateListResponse{}, ErrTenantIDRequired
	}
	return u.provider.ListTemplates(ctx, domain.WhatsAppTemplateListRequest{TenantID: strings.TrimSpace(query.TenantID), Limit: query.Limit, After: strings.TrimSpace(query.After)})
}

type WhatsAppTemplateDeleter interface {
	DeleteTemplate(ctx context.Context, request domain.WhatsAppTemplateDeleteRequest) error
}

type DeleteWhatsAppTemplateUseCase struct {
	provider WhatsAppTemplateDeleter
}

func NewDeleteWhatsAppTemplateUseCase(provider WhatsAppTemplateDeleter) *DeleteWhatsAppTemplateUseCase {
	return &DeleteWhatsAppTemplateUseCase{provider: provider}
}

func (u *DeleteWhatsAppTemplateUseCase) Execute(ctx context.Context, tenantID, name string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantIDRequired
	}
	if strings.TrimSpace(name) == "" {
		return ErrWhatsAppTemplateNameRequired
	}
	return u.provider.DeleteTemplate(ctx, domain.WhatsAppTemplateDeleteRequest{TenantID: strings.TrimSpace(tenantID), Name: strings.TrimSpace(name)})
}

func normalizeWhatsAppTemplateType(raw string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	if normalized == "" {
		return WhatsAppTemplateTypeOther, nil
	}
	switch normalized {
	case WhatsAppTemplateTypeJobAssignment, WhatsAppTemplateTypeOther:
		return normalized, nil
	default:
		return "", ErrWhatsAppTemplateUnsupportedType
	}
}

func buildWhatsAppTemplateComponents(header, body string) []map[string]any {
	trimmedHeader := strings.TrimSpace(header)
	trimmedBody := strings.TrimSpace(body)
	if trimmedHeader == "" && trimmedBody == "" {
		return nil
	}

	components := make([]map[string]any, 0, 2)
	if trimmedHeader != "" {
		components = append(components, map[string]any{
			"type":   "HEADER",
			"format": "TEXT",
			"text":   trimmedHeader,
		})
	}
	if trimmedBody != "" {
		components = append(components, map[string]any{
			"type": "BODY",
			"text": trimmedBody,
		})
	}
	return components
}

func normalizeWhatsAppTemplateComponents(components []map[string]any) []map[string]any {
	if len(components) == 0 {
		return nil
	}
	normalized := make([]map[string]any, 0, len(components))
	for _, component := range components {
		if component == nil {
			continue
		}
		normalized = append(normalized, component)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func hasMinimumJobAssignmentPlaceholders(components []map[string]any) bool {
	encoded, err := json.Marshal(components)
	if err != nil {
		return false
	}
	matches := whatsAppTemplatePlaceholderPattern.FindAllStringSubmatch(string(encoded), -1)
	if len(matches) == 0 {
		return false
	}
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		seen[match[1]] = struct{}{}
	}
	for index := 1; index <= jobAssignmentMinimumVariableCount; index++ {
		if _, ok := seen[strconv.Itoa(index)]; !ok {
			return false
		}
	}
	return true
}
