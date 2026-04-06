package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

func buildOtherTemplateUseCase(templateProvider *fakeWhatsAppTemplateProvider, messageProvider *fakeWhatsAppTemplateMessageProvider) (*SendOtherTemplateUseCase, *fakeWhatsAppConversationRepository) {
	conversationRepository := newFakeWhatsAppConversationRepository()
	return NewSendOtherTemplateUseCase(
		newFakeOutboxRepository(),
		conversationRepository,
		templateProvider,
		messageProvider,
		&fakeDeliveryPublisher{},
		fixedNotificationClock{now: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "other-1"},
		"default",
	), conversationRepository
}

func TestSendOtherTemplateUseCaseSendsApprovedTemplateWithAllVariables(t *testing.T) {
	templateProvider := &fakeWhatsAppTemplateProvider{
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{{
			ID:       "tpl-2",
			Name:     "driver_followup_v1",
			Language: "en_US",
			Status:   "APPROVED",
			Components: []map[string]any{
				{"type": "BODY", "text": "Hello {{1}}, report to {{2}} before {{3}}"},
			},
		}}},
	}
	messageProvider := &fakeWhatsAppTemplateMessageProvider{result: domain.WhatsAppSendResult{Status: domain.StatusSent, MessageID: "wamid.template.other.1"}}
	useCase, conversationRepository := buildOtherTemplateUseCase(templateProvider, messageProvider)

	message, err := useCase.Execute(context.Background(), SendOtherTemplateCommand{
		TenantID:     "tenant-1",
		Recipient:    "+2348012345678",
		TemplateName: "driver_followup_v1",
		Variables: map[string]string{
			"1": "Chukwuma Okafor",
			"2": "Ikeja Yard",
			"3": "6:30 PM",
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if message.Status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", message.Status)
	}
	params, ok := messageProvider.lastRequest.Components[0]["parameters"].([]map[string]any)
	if !ok || len(params) != 3 {
		t.Fatalf("expected 3 body parameters, got %#v", messageProvider.lastRequest.Components[0]["parameters"])
	}
	if params[1]["text"] != "Ikeja Yard" {
		t.Fatalf("expected second variable to be Ikeja Yard, got %#v", params[1])
	}
	conversation, ok, err := conversationRepository.FindConversationByTenantAndDriverPhone(context.Background(), "tenant-1", "2348012345678")
	if err != nil {
		t.Fatalf("find conversation: %v", err)
	}
	if !ok {
		t.Fatal("expected conversation to be created")
	}
	if !conversation.IsLocked(time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("expected conversation to be locked after template send")
	}
}

func TestSendOtherTemplateUseCaseRejectsMissingConfiguredVariables(t *testing.T) {
	templateProvider := &fakeWhatsAppTemplateProvider{
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{{
			Name:       "driver_followup_v1",
			Status:     "APPROVED",
			Components: []map[string]any{{"type": "BODY", "text": "Hello {{1}}, report to {{2}}"}},
		}}},
	}
	useCase, _ := buildOtherTemplateUseCase(templateProvider, &fakeWhatsAppTemplateMessageProvider{result: domain.WhatsAppSendResult{Status: domain.StatusSent}})

	_, err := useCase.Execute(context.Background(), SendOtherTemplateCommand{
		TenantID:     "tenant-1",
		Recipient:    "+2348012345678",
		TemplateName: "driver_followup_v1",
		Variables: map[string]string{
			"1": "Driver",
		},
	})
	if errors.Is(err, ErrOtherTemplateVariableRequired) == false {
		t.Fatalf("expected missing variable error, got %v", err)
	}
}

func TestSendOtherTemplateUseCaseRejectsJobAssignmentTemplate(t *testing.T) {
	templateProvider := &fakeWhatsAppTemplateProvider{
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{{
			Name:       "job_assignment_v1",
			Status:     "APPROVED",
			Components: []map[string]any{{"type": "BODY", "text": "Driver {{1}}, job {{2}} pickup {{3}} delivery {{4}} cargo {{5}} priority {{6}}"}},
		}}},
	}
	useCase, _ := buildOtherTemplateUseCase(templateProvider, &fakeWhatsAppTemplateMessageProvider{result: domain.WhatsAppSendResult{Status: domain.StatusSent}})

	_, err := useCase.Execute(context.Background(), SendOtherTemplateCommand{
		TenantID:     "tenant-1",
		Recipient:    "+2348012345678",
		TemplateName: "job_assignment_v1",
		Variables: map[string]string{
			"1": "Driver",
			"2": "TM-1",
			"3": "Pickup",
			"4": "Dropoff",
			"5": "Cargo",
			"6": "high",
		},
	})
	if errors.Is(err, ErrOtherTemplateJobAssignmentNotAllowed) == false {
		t.Fatalf("expected job assignment rejection, got %v", err)
	}
}
