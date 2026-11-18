package application

import (
	"context"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// PushDeviceRepository keeps the app installs that receive pushes.
type PushDeviceRepository interface {
	SaveDevice(ctx context.Context, device domain.PushDevice) error
	DeleteUserDevice(ctx context.Context, tenantID, userID, token string) error
	DeleteToken(ctx context.Context, token string) error
	TokensFor(ctx context.Context, tenantID, userID string) ([]string, error)
	UsersWithDevices(ctx context.Context, tenantID string) (map[string]bool, error)
}

type PushDevicesUseCase struct {
	repository PushDeviceRepository
	clock      Clock
}

func NewPushDevicesUseCase(repository PushDeviceRepository, clock Clock) *PushDevicesUseCase {
	return &PushDevicesUseCase{repository: repository, clock: clock}
}

func (u *PushDevicesUseCase) Register(ctx context.Context, device domain.PushDevice) error {
	if err := device.Validate(); err != nil {
		return err
	}
	now := u.clock.Now()
	device.CreatedAt, device.LastSeenAt = now, now
	return u.repository.SaveDevice(ctx, device)
}

// Unregister removes the token only while it still belongs to the caller, so a
// late sign-out on a handed-over phone cannot silence its new owner.
func (u *PushDevicesUseCase) Unregister(ctx context.Context, tenantID, userID, token string) error {
	if token == "" {
		return domain.ErrInvalidPushDevice
	}
	return u.repository.DeleteUserDevice(ctx, tenantID, userID, token)
}
