package application

import (
	"context"
	"log/slog"
	"testing"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

func TestConsumeJobOfferNotificationDefersWithoutRecipient(t *testing.T) {
	uc := NewConsumeJobOfferNotificationUseCase(nil, newFakeEventClaims(), slog.Default())
	err := uc.Handle(context.Background(), eventbus.EventEnvelope{Type: "job.offer.created", TenantID: "tenant-a", Data: map[string]any{"offer_id": "offer-1"}})
	if err != nil {
		t.Fatalf("expected recipient-less event to be acknowledged, got %v", err)
	}
}
