package application

import (
	"context"
	"errors"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeWhatsAppTemplateProvider struct {
	createResponse domain.WhatsAppTemplateCreateResponse
	listResponse   domain.WhatsAppTemplateListResponse
	createErr      error
	listErr        error
	lastCreate     domain.WhatsAppTemplateCreateRequest
	lastList       domain.WhatsAppTemplateListRequest
}

func (f *fakeWhatsAppTemplateProvider) CreateTemplate(_ context.Context, request domain.WhatsAppTemplateCreateRequest) (domain.WhatsAppTemplateCreateResponse, error) {
	f.lastCreate = request
	return f.createResponse, f.createErr
}

func (f *fakeWhatsAppTemplateProvider) ListTemplates(_ context.Context, request domain.WhatsAppTemplateListRequest) (domain.WhatsAppTemplateListResponse, error) {
	f.lastList = request
	return f.listResponse, f.listErr
}

func TestCreateWhatsAppTemplateUseCaseValidatesRequiredFields(t *testing.T) {
	provider := &fakeWhatsAppTemplateProvider{}
	useCase := NewCreateWhatsAppTemplateUseCase(provider)

	_, err := useCase.Execute(context.Background(), CreateWhatsAppTemplateCommand{TenantID: "tenant-1"})
	if !errors.Is(err, ErrWhatsAppTemplateNameRequired) {
		t.Fatalf("expected name required error, got %v", err)
	}

	_, err = useCase.Execute(context.Background(), CreateWhatsAppTemplateCommand{TenantID: "tenant-1", Name: "job_instruction_v1"})
	if !errors.Is(err, ErrWhatsAppTemplateBodyOrComponentsRequired) {
		t.Fatalf("expected body or components required error, got %v", err)
	}

	_, err = useCase.Execute(context.Background(), CreateWhatsAppTemplateCommand{TenantID: "tenant-1", Name: "job_instruction_v1", TemplateType: "invalid", Body: "Hello {{1}}"})
	if !errors.Is(err, ErrWhatsAppTemplateUnsupportedType) {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func TestCreateWhatsAppTemplateUseCaseTransformsSimplePayloadToMetaRequest(t *testing.T) {
	provider := &fakeWhatsAppTemplateProvider{createResponse: domain.WhatsAppTemplateCreateResponse{ID: "tpl-1", Status: "PENDING", Category: "UTILITY"}}
	useCase := NewCreateWhatsAppTemplateUseCase(provider)

	response, err := useCase.Execute(context.Background(), CreateWhatsAppTemplateCommand{
		TenantID: "tenant-1",
		Name:     " dispatch_job_instruction ",
		Header:   " Dispatch update ",
		Body:     " Hello {{1}} ",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if response.ID != "tpl-1" {
		t.Fatalf("expected template id tpl-1, got %q", response.ID)
	}
	if provider.lastCreate.Name != "dispatch_job_instruction" {
		t.Fatalf("expected trimmed name, got %q", provider.lastCreate.Name)
	}
	if provider.lastCreate.Language != "en_US" {
		t.Fatalf("expected default language en_US, got %q", provider.lastCreate.Language)
	}
	if provider.lastCreate.Category != "UTILITY" {
		t.Fatalf("expected default category UTILITY, got %q", provider.lastCreate.Category)
	}
	if provider.lastCreate.AllowCategoryChange == nil || !*provider.lastCreate.AllowCategoryChange {
		t.Fatal("expected allow_category_change to default to true")
	}
	if provider.lastCreate.ParameterFormat != "POSITIONAL" {
		t.Fatalf("expected POSITIONAL parameter format, got %q", provider.lastCreate.ParameterFormat)
	}
	if len(provider.lastCreate.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(provider.lastCreate.Components))
	}
	if provider.lastCreate.Components[0]["type"] != "HEADER" {
		t.Fatalf("expected HEADER component, got %#v", provider.lastCreate.Components[0])
	}
	if provider.lastCreate.Components[1]["type"] != "BODY" {
		t.Fatalf("expected BODY component, got %#v", provider.lastCreate.Components[1])
	}
}

func TestCreateWhatsAppTemplateUseCaseRequiresJobAssignmentVariables(t *testing.T) {
	provider := &fakeWhatsAppTemplateProvider{}
	useCase := NewCreateWhatsAppTemplateUseCase(provider)

	_, err := useCase.Execute(context.Background(), CreateWhatsAppTemplateCommand{
		TenantID:     "tenant-1",
		TemplateType: WhatsAppTemplateTypeJobAssignment,
		Name:         "job_assignment_v1",
		Components: []map[string]any{
			{"type": "BODY", "text": "Driver {{1}}, job {{2}} from {{3}} to {{4}}"},
		},
	})
	if !errors.Is(err, ErrWhatsAppTemplateJobAssignmentVariablesRequired) {
		t.Fatalf("expected job assignment placeholder validation error, got %v", err)
	}
}

func TestCreateWhatsAppTemplateUseCaseSupportsRichComponents(t *testing.T) {
	provider := &fakeWhatsAppTemplateProvider{createResponse: domain.WhatsAppTemplateCreateResponse{ID: "tpl-2", Status: "PENDING", Category: "UTILITY"}}
	useCase := NewCreateWhatsAppTemplateUseCase(provider)

	allowCategoryChange := false
	components := []map[string]any{
		{
			"type":   "HEADER",
			"format": "IMAGE",
			"example": map[string]any{
				"header_handle": []string{"https://example.com/template-header.png"},
			},
		},
		{
			"type": "BODY",
			"text": "Driver {{1}}, job {{2}} pickup {{3}} delivery {{4}} cargo {{5}} priority {{6}}",
		},
		{
			"type": "BUTTONS",
			"buttons": []map[string]any{
				{"type": "QUICK_REPLY", "text": "Acknowledge"},
				{"type": "URL", "text": "Open Job", "url": "https://truckmoov.com/jobs/{{2}}"},
			},
		},
	}

	response, err := useCase.Execute(context.Background(), CreateWhatsAppTemplateCommand{
		TenantID:            "tenant-1",
		TemplateType:        "job assignment",
		Name:                "job_assignment_v2",
		Language:            "en_GB",
		Category:            "UTILITY",
		AllowCategoryChange: &allowCategoryChange,
		ParameterFormat:     "POSITIONAL",
		Components:          components,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if response.ID != "tpl-2" {
		t.Fatalf("expected template id tpl-2, got %q", response.ID)
	}
	if provider.lastCreate.Language != "en_GB" {
		t.Fatalf("expected language en_GB, got %q", provider.lastCreate.Language)
	}
	if provider.lastCreate.AllowCategoryChange == nil || *provider.lastCreate.AllowCategoryChange {
		t.Fatal("expected explicit allow_category_change=false to pass through")
	}
	if len(provider.lastCreate.Components) != len(components) {
		t.Fatalf("expected %d components, got %d", len(components), len(provider.lastCreate.Components))
	}
	if provider.lastCreate.Components[0]["format"] != "IMAGE" {
		t.Fatalf("expected header media component to pass through, got %#v", provider.lastCreate.Components[0])
	}
	if provider.lastCreate.Components[2]["type"] != "BUTTONS" {
		t.Fatalf("expected buttons component to pass through, got %#v", provider.lastCreate.Components[2])
	}
	buttons, ok := provider.lastCreate.Components[2]["buttons"].([]map[string]any)
	if !ok || len(buttons) != 2 {
		t.Fatalf("expected buttons to pass through, got %#v", provider.lastCreate.Components[2]["buttons"])
	}
	if buttons[1]["type"] != "URL" {
		t.Fatalf("expected URL button, got %#v", buttons[1])
	}
}

func TestListWhatsAppTemplatesUseCaseRequiresTenant(t *testing.T) {
	provider := &fakeWhatsAppTemplateProvider{}
	useCase := NewListWhatsAppTemplatesUseCase(provider)

	_, err := useCase.Execute(context.Background(), ListWhatsAppTemplatesQuery{})
	if !errors.Is(err, ErrTenantIDRequired) {
		t.Fatalf("expected tenant required error, got %v", err)
	}
}
