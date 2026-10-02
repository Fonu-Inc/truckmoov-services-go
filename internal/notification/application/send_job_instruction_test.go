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

func approvedJobTemplates() *fakeWhatsAppTemplateProvider {
	return &fakeWhatsAppTemplateProvider{
		listResponse: domain.WhatsAppTemplateListResponse{Data: []domain.WhatsAppTemplate{{
			Name:       "job_assignment_v1",
			Language:   "en_US",
			Status:     "APPROVED",
			Components: []map[string]any{{"type": "BODY", "text": "Driver {{1}}, job {{2}} pickup {{3}} delivery {{4}} cargo {{5}} priority {{6}}"}},
		}}},
	}
}

func jobInstructionCommand() SendJobInstructionCommand {
	return SendJobInstructionCommand{
		TenantID: "tenant-1", JobID: "job-9", DriverID: "driver-9", Recipient: "+2348012345678", TemplateName: "job_assignment_v1",
		DriverName: "Chukwuma Okafor", JobReference: "TM-9", PickupLocation: "Apapa", DeliveryLocation: "Ikeja", CargoDescription: "Electronics", Priority: "high",
	}
}

func jobInstructionWithLog(provider *fakeWhatsAppTemplateMessageProvider, templates *fakeWhatsAppTemplateProvider) (*SendJobInstructionUseCase, *recordingDeliveryLog, *fakeOutboxRepository) {
	log := &recordingDeliveryLog{}
	outbox := newFakeOutboxRepository()
	useCase := NewSendJobInstructionUseCase(
		outbox, newFakeWhatsAppConversationRepository(), templates, provider, &fakeDeliveryPublisher{},
		fixedNotificationClock{now: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)}, fixedNotificationIDGenerator{id: "9"}, "default",
	).WithDeliveryLog(log)
	return useCase, log, outbox
}

func TestSendJobInstructionWritesTheSentTemplateToTheDeliveryLog(t *testing.T) {
	useCase, log, _ := jobInstructionWithLog(&fakeWhatsAppTemplateMessageProvider{}, approvedJobTemplates())

	if _, err := useCase.Execute(context.Background(), jobInstructionCommand()); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(log.entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(log.entries))
	}
	entry := log.entries[0]
	if entry.TenantID != "tenant-1" || entry.NotificationType != domain.NotificationTypeJobAssignment || entry.Channel != "whatsapp" ||
		entry.Status != domain.StatusSent || entry.ProviderMessageID != "wamid.template.1" {
		t.Fatalf("unexpected entry %+v", entry)
	}
}

func TestSendJobInstructionLogsAProviderFailure(t *testing.T) {
	useCase, log, _ := jobInstructionWithLog(&fakeWhatsAppTemplateMessageProvider{err: errors.New("provider down")}, approvedJobTemplates())

	if _, err := useCase.Execute(context.Background(), jobInstructionCommand()); err == nil {
		t.Fatal("expected the failure")
	}

	if len(log.entries) != 1 || log.entries[0].Status != domain.StatusFailed || log.entries[0].FailureReason != "provider down" {
		t.Fatalf("expected a failed entry with the reason, got %+v", log.entries)
	}
}

func TestSendJobInstructionLogsARefusedWallet(t *testing.T) {
	useCase, log, _ := jobInstructionWithLog(&fakeWhatsAppTemplateMessageProvider{}, approvedJobTemplates())
	command := jobInstructionCommand()
	command.BeforeSend = func(context.Context) error { return ErrInsufficientFunds }

	if _, err := useCase.Execute(context.Background(), command); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("expected the refusal, got %v", err)
	}

	if len(log.entries) != 1 || log.entries[0].Status != domain.StatusFailed {
		t.Fatalf("expected a failed entry, got %+v", log.entries)
	}
}

func TestSendJobInstructionLogsATemplateThatCannotBeUsed(t *testing.T) {
	templates := approvedJobTemplates()
	templates.listResponse.Data[0].Status = "PENDING"
	useCase, log, _ := jobInstructionWithLog(&fakeWhatsAppTemplateMessageProvider{}, templates)

	if _, err := useCase.Execute(context.Background(), jobInstructionCommand()); !errors.Is(err, ErrJobInstructionTemplateNotApproved) {
		t.Fatalf("expected not approved, got %v", err)
	}

	if len(log.entries) != 1 || log.entries[0].Status != domain.StatusFailed || log.entries[0].TenantID != "tenant-1" {
		t.Fatalf("an instruction that could not go out is still logged, got %+v", log.entries)
	}
}

func TestSendJobInstructionRedeliveryDoesNotSendTheTemplateAgain(t *testing.T) {
	provider := &fakeWhatsAppTemplateMessageProvider{}
	useCase, log, outbox := jobInstructionWithLog(provider, approvedJobTemplates())
	command := jobInstructionCommand()
	command.MessageID, command.ConversationMessageID = "msg-evt-1", "wam-evt-1"
	var charges int
	command.BeforeSend = func(context.Context) error { charges++; return nil }

	if _, err := useCase.Execute(context.Background(), command); err != nil {
		t.Fatalf("first send: %v", err)
	}
	provider.lastRequest = domain.WhatsAppTemplateMessageRequest{}
	message, err := useCase.Execute(context.Background(), command)

	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if provider.lastRequest.Name != "" || charges != 1 {
		t.Fatalf("the template must not be sent or charged twice: request=%q charges=%d", provider.lastRequest.Name, charges)
	}
	if stored, ok := outbox.FindByTenantAndID("tenant-1", "msg-evt-1"); !ok || message.ProviderMessageID != stored.ProviderMessageID {
		t.Fatalf("expected the stored message back, got %+v", message)
	}
	if len(log.entries) != 1 {
		t.Fatalf("expected a single log entry, got %d", len(log.entries))
	}
}

func TestSendJobInstructionRecordsTheConversationUnderTheGivenId(t *testing.T) {
	useCase, _, _ := jobInstructionWithLog(&fakeWhatsAppTemplateMessageProvider{}, approvedJobTemplates())
	command := jobInstructionCommand()
	command.ConversationMessageID = "wam-evt-2"
	repository := newFakeWhatsAppConversationRepository()
	useCase.conversationRepository = repository

	for range 2 {
		if _, err := useCase.Execute(context.Background(), command); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	conversation, _, _ := repository.FindConversationByTenantAndDriverPhone(context.Background(), "tenant-1", "2348012345678")
	messages, _ := repository.ListMessagesByConversation(context.Background(), "tenant-1", conversation.ID)
	if len(messages) != 1 || messages[0].ID != "wam-evt-2" {
		t.Fatalf("expected one message under the given id, got %+v", messages)
	}
}

type failingConversationRepository struct {
	*fakeWhatsAppConversationRepository
	fail bool
}

func (r *failingConversationRepository) SaveMessage(ctx context.Context, message domain.WhatsAppConversationMessage) (domain.WhatsAppConversationMessage, error) {
	if r.fail {
		return domain.WhatsAppConversationMessage{}, errors.New("db down")
	}
	return r.fakeWhatsAppConversationRepository.SaveMessage(ctx, message)
}

func TestSendJobInstructionRecordFailureIsRetriedWithoutSendingTwice(t *testing.T) {
	provider := &fakeWhatsAppTemplateMessageProvider{}
	useCase, _, _ := jobInstructionWithLog(provider, approvedJobTemplates())
	repository := &failingConversationRepository{fakeWhatsAppConversationRepository: newFakeWhatsAppConversationRepository(), fail: true}
	useCase.conversationRepository = repository
	command := jobInstructionCommand()
	command.MessageID, command.ConversationMessageID = "msg-evt-3", "wam-evt-3"

	_, err := useCase.Execute(context.Background(), command)
	var unrecorded *ConversationRecordError
	if !errors.As(err, &unrecorded) {
		t.Fatalf("expected a record error, got %v", err)
	}

	provider.lastRequest = domain.WhatsAppTemplateMessageRequest{}
	repository.fail = false
	if _, err := useCase.Execute(context.Background(), command); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if provider.lastRequest.Name != "" {
		t.Fatal("the retry must only record, not send again")
	}
	conversation, _, _ := repository.FindConversationByTenantAndDriverPhone(context.Background(), "tenant-1", "2348012345678")
	messages, _ := repository.ListMessagesByConversation(context.Background(), "tenant-1", conversation.ID)
	if len(messages) != 1 || messages[0].ID != "wam-evt-3" {
		t.Fatalf("expected the instruction recorded once, got %+v", messages)
	}
}
