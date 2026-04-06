package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeWhatsAppTemplateMessageProvider struct {
	result      domain.WhatsAppSendResult
	err         error
	lastRequest domain.WhatsAppTemplateMessageRequest
}

func (f *fakeWhatsAppTemplateMessageProvider) SendTemplate(_ context.Context, request domain.WhatsAppTemplateMessageRequest) (domain.WhatsAppSendResult, error) {
	f.lastRequest = request
	result := f.result
	if result.Status == "" {
		result.Status = domain.StatusSent
	}
	if result.MessageID == "" {
		result.MessageID = "wamid.template.1"
	}
	if f.err != nil {
		result.Status = domain.StatusFailed
	}
	return result, f.err
}

func buildJobInstructionUseCase(templateProvider *fakeWhatsAppTemplateProvider, messageProvider *fakeWhatsAppTemplateMessageProvider) (*SendJobInstructionUseCase, *fakeWhatsAppConversationRepository) {
	conversationRepository := newFakeWhatsAppConversationRepository()
	return NewSendJobInstructionUseCase(
		newFakeOutboxRepository(),
		conversationRepository,
		templateProvider,
		messageProvider,
		&fakeDeliveryPublisher{},
		fixedNotificationClock{now: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "job-1"},
		"default",
	), conversationRepository
}

func TestSendJobInstructionUseCaseSendsApprovedJobAssignmentTemplate(t *testing.T) {
	templateProvider := &fakeWhatsAppTemplateProvider{
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{{
			ID:       "tpl-1",
			Name:     "job_assignment_v1",
			Language: "en_US",
			Status:   "APPROVED",
			Components: []map[string]any{
				{"type": "BODY", "text": "Driver {{1}}, job {{2}} pickup {{3}} delivery {{4}} cargo {{5}} priority {{6}}"},
			},
		}}},
	}
	messageProvider := &fakeWhatsAppTemplateMessageProvider{result: domain.WhatsAppSendResult{Status: domain.StatusSent, MessageID: "wamid.template.1"}}
	useCase, conversationRepository := buildJobInstructionUseCase(templateProvider, messageProvider)

	message, err := useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:         "tenant-1",
		Recipient:        "+2348012345678",
		TemplateName:     "job_assignment_v1",
		DriverName:       "Chukwuma Okafor",
		JobReference:     "TM-2847",
		PickupLocation:   "Apapa Port, Lagos",
		DeliveryLocation: "Ikeja Industrial Estate",
		CargoDescription: "Electronics, 50kg",
		Priority:         "urgent",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if message.Status != domain.StatusSent {
		t.Fatalf("expected sent status, got %s", message.Status)
	}
	if messageProvider.lastRequest.Name != "job_assignment_v1" {
		t.Fatalf("expected template name job_assignment_v1, got %q", messageProvider.lastRequest.Name)
	}
	if len(messageProvider.lastRequest.Components) != 1 {
		t.Fatalf("expected 1 send component, got %d", len(messageProvider.lastRequest.Components))
	}
	params, ok := messageProvider.lastRequest.Components[0]["parameters"].([]map[string]any)
	if !ok || len(params) != 6 {
		t.Fatalf("expected 6 body parameters, got %#v", messageProvider.lastRequest.Components[0]["parameters"])
	}
	if params[1]["text"] != "TM-2847" {
		t.Fatalf("expected job reference parameter, got %#v", params[1])
	}
	if templateProvider.lastList.Limit != 100 {
		t.Fatalf("expected template lookup to request page size 100, got %d", templateProvider.lastList.Limit)
	}
	conversation, ok, err := conversationRepository.FindConversationByTenantAndDriverPhone(context.Background(), "tenant-1", "2348012345678")
	if err != nil {
		t.Fatalf("find conversation: %v", err)
	}
	if !ok {
		t.Fatal("expected conversation to be created")
	}
	if !conversation.IsLocked(time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("expected conversation to remain locked after template send")
	}
}

func TestSendJobInstructionUseCaseRejectsTemplateThatIsNotApproved(t *testing.T) {
	templateProvider := &fakeWhatsAppTemplateProvider{
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{{
			Name:       "job_assignment_v1",
			Status:     "PENDING",
			Components: []map[string]any{{"type": "BODY", "text": "Driver {{1}} job {{2}} pickup {{3}} delivery {{4}} cargo {{5}} priority {{6}}"}},
		}}},
	}
	useCase, _ := buildJobInstructionUseCase(templateProvider, &fakeWhatsAppTemplateMessageProvider{result: domain.WhatsAppSendResult{Status: domain.StatusSent}})

	_, err := useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:         "tenant-1",
		Recipient:        "+2348012345678",
		TemplateName:     "job_assignment_v1",
		DriverName:       "Driver",
		JobReference:     "TM-1",
		PickupLocation:   "Pickup",
		DeliveryLocation: "Dropoff",
		CargoDescription: "Cargo",
		Priority:         "high",
	})
	if errors.Is(err, ErrJobInstructionTemplateNotApproved) == false {
		t.Fatalf("expected template not approved error, got %v", err)
	}
}

func TestSendJobInstructionUseCaseRejectsTemplateWithoutRequiredPlaceholders(t *testing.T) {
	templateProvider := &fakeWhatsAppTemplateProvider{
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{{
			Name:       "job_assignment_v1",
			Status:     "APPROVED",
			Components: []map[string]any{{"type": "BODY", "text": "Driver {{1}} job {{2}}"}},
		}}},
	}
	useCase, _ := buildJobInstructionUseCase(templateProvider, &fakeWhatsAppTemplateMessageProvider{result: domain.WhatsAppSendResult{Status: domain.StatusSent}})

	_, err := useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:         "tenant-1",
		Recipient:        "+2348012345678",
		TemplateName:     "job_assignment_v1",
		DriverName:       "Driver",
		JobReference:     "TM-1",
		PickupLocation:   "Pickup",
		DeliveryLocation: "Dropoff",
		CargoDescription: "Cargo",
		Priority:         "high",
	})
	if errors.Is(err, ErrJobInstructionTemplateNotCompatible) == false {
		t.Fatalf("expected template not compatible error, got %v", err)
	}
}

func TestSendJobInstructionUseCaseRequiresHeaderMediaWhenTemplateNeedsIt(t *testing.T) {
	templateProvider := &fakeWhatsAppTemplateProvider{
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{{
			Name:       "job_assignment_v1",
			Status:     "APPROVED",
			Components: []map[string]any{{"type": "HEADER", "format": "IMAGE"}, {"type": "BODY", "text": "Driver {{1}} job {{2}} pickup {{3}} delivery {{4}} cargo {{5}} priority {{6}}"}},
		}}},
	}
	useCase, _ := buildJobInstructionUseCase(templateProvider, &fakeWhatsAppTemplateMessageProvider{result: domain.WhatsAppSendResult{Status: domain.StatusSent}})

	_, err := useCase.Execute(context.Background(), SendJobInstructionCommand{
		TenantID:         "tenant-1",
		Recipient:        "+2348012345678",
		TemplateName:     "job_assignment_v1",
		DriverName:       "Driver",
		JobReference:     "TM-1",
		PickupLocation:   "Pickup",
		DeliveryLocation: "Dropoff",
		CargoDescription: "Cargo",
		Priority:         "high",
	})
	if errors.Is(err, ErrJobInstructionHeaderMediaRequired) == false {
		t.Fatalf("expected header media required error, got %v", err)
	}
}
