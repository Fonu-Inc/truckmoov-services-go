package application

import (
	"context"

	"github.com/truckmoov/services-go/pkg/identity"
)

// PlatformMessagingReadUseCase marks a platform conversation read. Read state
// is shared by all staff.
type PlatformMessagingReadUseCase struct {
	repository PlatformMessagingRepository
	notifier   *PlatformMessagingNotifier
	clock      Clock
}

func NewPlatformMessagingReadUseCase(repository PlatformMessagingRepository, notifier *PlatformMessagingNotifier, clock Clock) *PlatformMessagingReadUseCase {
	return &PlatformMessagingReadUseCase{repository: repository, notifier: notifier, clock: clock}
}

func (u *PlatformMessagingReadUseCase) Execute(ctx context.Context, conversationID string) error {
	tenantID := identity.SystemTenantID
	if err := u.repository.MarkConversationRead(ctx, tenantID, conversationID, u.clock.Now().UTC()); err != nil {
		return err
	}
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, tenantID, conversationID)
	if err != nil || !ok {
		return err
	}
	u.notifier.ConversationUpdated(ctx, conversation)
	return nil
}
