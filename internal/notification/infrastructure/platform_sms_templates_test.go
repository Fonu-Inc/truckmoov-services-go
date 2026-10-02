package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

type recordingSMS struct{ sent []string }

func (r *recordingSMS) Send(_ context.Context, tenantID, channel, recipient, content string) (string, error) {
	r.sent = append(r.sent, fmt.Sprintf("%s|%s|%s|%s", tenantID, channel, recipient, content))
	return "accepted", nil
}

func TestSMSTemplatesValidateExtractVariablesAndKeepNamesUnique(t *testing.T) {
	templates := application.NewPlatformSMSTemplates(NewInMemorySMSTemplateRepository(), &recordingSMS{}, &testClock{now: inboxEpoch}, &countingIDs{})
	ctx := context.Background()

	saved, err := templates.Save(ctx, application.SMSTemplateInput{Name: " Downtime ", Content: "Hi {{name}}, {{ date }} downtime. Bye {{name}}.", ActorID: "u-1"})
	if err != nil || saved.Category != "general" || !saved.IsActive || fmt.Sprint(saved.Variables) != "[name date]" || saved.CreatedBy != "u-1" {
		t.Fatalf("save: %+v err=%v", saved, err)
	}
	if _, err := templates.Save(ctx, application.SMSTemplateInput{Name: "DOWNTIME", Content: "x"}); !errors.Is(err, application.ErrSMSTemplateNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	var input application.InboxInputError
	for label, bad := range map[string]application.SMSTemplateInput{
		"blank name":   {Name: " ", Content: "x"},
		"bad category": {Name: "a", Category: "spam", Content: "x"},
		"no content":   {Name: "a", Content: " "},
		"too long":     {Name: "a", Content: fmt.Sprintf("%0481d", 0)},
	} {
		if _, err := templates.Save(ctx, bad); !errors.As(err, &input) {
			t.Errorf("%s: %v", label, err)
		}
	}

	off := false
	updated, err := templates.Save(ctx, application.SMSTemplateInput{ID: saved.ID, Name: "Downtime v2", Content: "Hi {{name}}", IsActive: &off})
	if err != nil || updated.IsActive || updated.CreatedBy != "u-1" || fmt.Sprint(updated.Variables) != "[name]" {
		t.Fatalf("update: %+v err=%v", updated, err)
	}
	kept, _ := templates.Save(ctx, application.SMSTemplateInput{ID: saved.ID, Name: "Downtime v3", Content: "Hi {{name}}"})
	if kept.IsActive {
		t.Fatal("an update that does not mention is_active must keep it off")
	}
	if _, err := templates.Save(ctx, application.SMSTemplateInput{ID: "nope", Name: "z", Content: "z"}); !errors.Is(err, application.ErrSMSTemplateNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if err := templates.Delete(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := templates.Delete(ctx, saved.ID); !errors.Is(err, application.ErrSMSTemplateNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestSMSTemplateTestSendFillsEveryPlaceholder(t *testing.T) {
	provider := &recordingSMS{}
	templates := application.NewPlatformSMSTemplates(NewInMemorySMSTemplateRepository(), provider, &testClock{now: inboxEpoch}, &countingIDs{})
	ctx := context.Background()
	saved, _ := templates.Save(ctx, application.SMSTemplateInput{Name: "Hello", Content: "Hi {{name}}, from {{company}}."})

	var input application.InboxInputError
	if _, err := templates.Test(ctx, application.SMSTemplateTest{TemplateID: saved.ID, To: "08012345678", Values: map[string]string{"name": "Ada"}}); !errors.As(err, &input) {
		t.Fatalf("missing value: %v", err)
	}
	if _, err := templates.Test(ctx, application.SMSTemplateTest{TemplateID: saved.ID, To: "12", Values: map[string]string{"name": "Ada", "company": "X"}}); !errors.Is(err, application.ErrOutreachPhoneInvalid) {
		t.Fatalf("bad number: %v", err)
	}
	result, err := templates.Test(ctx, application.SMSTemplateTest{TemplateID: saved.ID, To: "2348012345678", Values: map[string]string{"name": "Ada", "company": "Truckmoov"}})
	if err != nil || result.Rendered != "Hi Ada, from Truckmoov." || len(provider.sent) != 1 || provider.sent[0] != "system|sms|2348012345678|Hi Ada, from Truckmoov." {
		t.Fatalf("test send: %+v sent=%v err=%v", result, provider.sent, err)
	}
}

func TestRenderSMSTemplateReportsWhatIsMissing(t *testing.T) {
	rendered, missing := domain.RenderSMSTemplate("{{a}} {{b}} {{a}}", map[string]string{"a": "1"})
	if rendered != "1 {{b}} 1" || fmt.Sprint(missing) != "[b]" {
		t.Fatalf("rendered=%q missing=%v", rendered, missing)
	}
}
