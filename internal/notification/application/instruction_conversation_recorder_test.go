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
	context, usable := driverStatusRequestContextFromMessage(message, domain.WhatsAppConversation{})
	if !usable || context.JobID != "job-1" || context.DriverID != "driver-1" {
		t.Fatalf("a reply must resolve to the job, got %+v usable=%v", context, usable)
	}
}
