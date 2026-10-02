package application

import (
	"context"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

func TestInstructionRecorderStoresATextInstructionTheWayTheTemplatePathDoes(t *testing.T) {
	repository := newFakeWhatsAppConversationRepository()
	recorder := NewInstructionConversationRecorder(repository, campaignClock{now: instructionNow}, &campaignIDs{})

	err := recorder.RecordJobInstructionText(context.Background(), JobInstructionTextRecord{
		TenantID: "tenant-a", JobID: "job-1", DriverID: "driver-1", Recipient: "+2348012345678",
		Text: "New job TM-1 assigned.", ProviderMessageID: "wamid.text-1", Status: domain.StatusSent,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	message, ok, _ := repository.FindMessageByProviderMessageID(context.Background(), "wamid.text-1")
	if !ok {
		t.Fatal("the message must be findable by its wamid so receipts land")
	}
	context, usable := driverStatusRequestContextFromMessage(message, domain.WhatsAppConversation{TenantID: "tenant-a"})
	if !usable || context.JobID != "job-1" || context.DriverID != "driver-1" {
		t.Fatalf("a reply must resolve to the job, got %+v usable=%v", context, usable)
	}
}

func TestInstructionRecorderRecordingTwiceOverwritesInsteadOfDuplicating(t *testing.T) {
	repository := newFakeWhatsAppConversationRepository()
	recorder := NewInstructionConversationRecorder(repository, campaignClock{now: instructionNow}, &campaignIDs{})
	record := JobInstructionTextRecord{
		TenantID: "tenant-a", JobID: "job-1", DriverID: "driver-1", Recipient: "+2348012345678",
		Text: "New job TM-1 assigned.", ProviderMessageID: "wamid.text-1", Status: domain.StatusSent, MessageID: "wam-evt-1",
	}

	for range 2 {
		if err := recorder.RecordJobInstructionText(context.Background(), record); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	conversation, _, _ := repository.FindConversationByTenantAndDriverPhone(context.Background(), "tenant-a", "2348012345678")
	messages, _ := repository.ListMessagesByConversation(context.Background(), "tenant-a", conversation.ID)
	if len(messages) != 1 || messages[0].ID != "wam-evt-1" {
		t.Fatalf("expected one message under the derived id, got %+v", messages)
	}
}

func TestAReplyDoesNotResolveToAnotherTenantsJobInstruction(t *testing.T) {
	repository := newFakeWhatsAppConversationRepository()
	recorder := NewInstructionConversationRecorder(repository, campaignClock{now: instructionNow}, &campaignIDs{})
	_ = recorder.RecordJobInstructionText(context.Background(), JobInstructionTextRecord{
		TenantID: "tenant-a", JobID: "job-1", DriverID: "driver-1", Recipient: "+2348012345678",
		Text: "New job", ProviderMessageID: "wamid.text-1", Status: domain.StatusSent,
	})
	message, _, _ := repository.FindMessageByProviderMessageID(context.Background(), "wamid.text-1")

	if _, usable := driverStatusRequestContextFromMessage(message, domain.WhatsAppConversation{TenantID: "tenant-b"}); usable {
		t.Fatal("an instruction of tenant-a must not give a job context to tenant-b")
	}
}
