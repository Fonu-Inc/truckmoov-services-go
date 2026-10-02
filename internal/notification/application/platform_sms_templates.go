package application

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

var (
	ErrSMSTemplateNotFound  = errors.New("sms template not found")
	ErrSMSTemplateNameTaken = errors.New("a template with that name already exists")
)

const (
	maxSMSTemplateName    = 80
	maxSMSTemplateContent = 480
)

type SMSTemplateRepository interface {
	EnsureSchema(ctx context.Context) error
	ListSMSTemplates(ctx context.Context) ([]domain.SMSTemplate, error)
	FindSMSTemplate(ctx context.Context, id string) (domain.SMSTemplate, bool, error)
	SaveSMSTemplate(ctx context.Context, template domain.SMSTemplate) (domain.SMSTemplate, error)
	DeleteSMSTemplate(ctx context.Context, id string) (bool, error)
}

type SMSTemplateInput struct {
	ID       string
	Name     string
	Category string
	Content  string
	IsActive *bool
	ActorID  string
}

type SMSTemplateTest struct {
	TemplateID string
	To         string
	Values     map[string]string
}

type SMSTemplateTestResult struct {
	Rendered string `json:"rendered"`
}

// PlatformSMSTemplates is the library of SMS texts staff reuse in campaigns.
type PlatformSMSTemplates struct {
	repository SMSTemplateRepository
	provider   MessageProvider
	clock      Clock
	ids        IDGenerator
}

func NewPlatformSMSTemplates(repository SMSTemplateRepository, provider MessageProvider, clock Clock, ids IDGenerator) *PlatformSMSTemplates {
	return &PlatformSMSTemplates{repository: repository, provider: provider, clock: clock, ids: ids}
}

func (u *PlatformSMSTemplates) List(ctx context.Context) ([]domain.SMSTemplate, error) {
	return u.repository.ListSMSTemplates(ctx)
}

func (u *PlatformSMSTemplates) Save(ctx context.Context, input SMSTemplateInput) (domain.SMSTemplate, error) {
	template, err := validSMSTemplate(input)
	if err != nil {
		return domain.SMSTemplate{}, err
	}
	now := u.clock.Now().UTC()
	template.UpdatedAt = now
	if id := strings.TrimSpace(input.ID); id != "" {
		existing, found, err := u.repository.FindSMSTemplate(ctx, id)
		if err != nil {
			return domain.SMSTemplate{}, err
		}
		if !found {
			return domain.SMSTemplate{}, ErrSMSTemplateNotFound
		}
		template.ID, template.CreatedAt, template.CreatedBy = existing.ID, existing.CreatedAt, existing.CreatedBy
		if input.IsActive == nil {
			template.IsActive = existing.IsActive
		}
		return u.repository.SaveSMSTemplate(ctx, template)
	}
	template.ID, template.CreatedAt, template.CreatedBy = u.ids.New("sms"), now, input.ActorID
	return u.repository.SaveSMSTemplate(ctx, template)
}

func (u *PlatformSMSTemplates) Delete(ctx context.Context, id string) error {
	deleted, err := u.repository.DeleteSMSTemplate(ctx, strings.TrimSpace(id))
	if err == nil && !deleted {
		err = ErrSMSTemplateNotFound
	}
	return err
}

// Test sends the filled-in template to one number, so staff can see it on a
// handset before a campaign uses it.
func (u *PlatformSMSTemplates) Test(ctx context.Context, test SMSTemplateTest) (SMSTemplateTestResult, error) {
	template, found, err := u.repository.FindSMSTemplate(ctx, strings.TrimSpace(test.TemplateID))
	if err != nil {
		return SMSTemplateTestResult{}, err
	}
	if !found {
		return SMSTemplateTestResult{}, ErrSMSTemplateNotFound
	}
	to := normalizeConversationPhone(test.To)
	if !outreachPhonePattern.MatchString(to) {
		return SMSTemplateTestResult{}, ErrOutreachPhoneInvalid
	}
	rendered, missing := domain.RenderSMSTemplate(template.Content, test.Values)
	if len(missing) > 0 {
		return SMSTemplateTestResult{}, InboxInputError("give a value for " + strings.Join(missing, ", "))
	}
	if _, err := u.provider.Send(ctx, identity.SystemTenantID, "sms", to, rendered); err != nil {
		return SMSTemplateTestResult{}, err
	}
	return SMSTemplateTestResult{Rendered: rendered}, nil
}

func validSMSTemplate(input SMSTemplateInput) (domain.SMSTemplate, error) {
	name := strings.TrimSpace(input.Name)
	content := strings.TrimSpace(input.Content)
	category := strings.ToLower(strings.TrimSpace(input.Category))
	if category == "" {
		category = "general"
	}
	switch {
	case name == "" || utf8.RuneCountInString(name) > maxSMSTemplateName:
		return domain.SMSTemplate{}, InboxInputError("name must be 1 to 80 characters")
	case !domain.IsSMSTemplateCategory(category):
		return domain.SMSTemplate{}, InboxInputError("category must be general, job, delivery, safety or payment")
	case content == "" || utf8.RuneCountInString(content) > maxSMSTemplateContent || hasDisallowedControl(content):
		return domain.SMSTemplate{}, InboxInputError("content must be 1 to 480 characters")
	}
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}
	return domain.SMSTemplate{Name: name, Category: category, Content: content, Variables: domain.SMSTemplateVariables(content), IsActive: active}, nil
}
