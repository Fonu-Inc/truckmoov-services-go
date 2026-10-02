package application

import (
	"context"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type NotificationPreferencesRepository interface {
	EnsureSchema(ctx context.Context) error
	FindPreferences(ctx context.Context, tenantID string) (domain.NotificationPreferences, bool, error)
	SavePreferences(ctx context.Context, preferences domain.NotificationPreferences) error
}

type NotificationPreferencesUseCase struct {
	repository NotificationPreferencesRepository
	clock      Clock
}

func NewNotificationPreferencesUseCase(repository NotificationPreferencesRepository, clock Clock) *NotificationPreferencesUseCase {
	return &NotificationPreferencesUseCase{repository: repository, clock: clock}
}

type UpdateNotificationPreferencesCommand struct {
	TenantID string
	Email    bool
	WhatsApp bool
	SMS      bool
}

func (u *NotificationPreferencesUseCase) Get(ctx context.Context, tenantID string) (domain.NotificationPreferences, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return domain.NotificationPreferences{}, ErrTenantIDRequired
	}
	preferences, found, err := u.repository.FindPreferences(ctx, tenantID)
	if err != nil || !found {
		return domain.DefaultNotificationPreferences(tenantID), err
	}
	return preferences, nil
}

func (u *NotificationPreferencesUseCase) Update(ctx context.Context, command UpdateNotificationPreferencesCommand) (domain.NotificationPreferences, error) {
	preferences := domain.NotificationPreferences{
		TenantID:  strings.TrimSpace(command.TenantID),
		Email:     command.Email,
		WhatsApp:  command.WhatsApp,
		SMS:       command.SMS,
		UpdatedAt: u.clock.Now().UTC(),
	}
	if err := preferences.Validate(); err != nil {
		return domain.NotificationPreferences{}, err
	}
	if err := u.repository.SavePreferences(ctx, preferences); err != nil {
		return domain.NotificationPreferences{}, err
	}
	return preferences, nil
}
