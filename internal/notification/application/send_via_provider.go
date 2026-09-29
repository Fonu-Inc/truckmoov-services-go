package application

import (
	"context"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// sendViaProvider sends one message and returns the id the provider gave it,
// when the provider gives one. Every caller that stores a sent message goes
// through here so the id is kept the same way each time.
func sendViaProvider(ctx context.Context, provider MessageProvider, tenantID, channel, recipient, content string) (domain.MessageSendResult, error) {
	if tracked, ok := provider.(TrackedMessageProvider); ok {
		return tracked.SendTracked(ctx, tenantID, channel, recipient, content)
	}
	status, err := provider.Send(ctx, tenantID, channel, recipient, content)
	return domain.MessageSendResult{Status: status}, err
}
