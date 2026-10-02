package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakePreferencesRepository struct {
	stored map[string]domain.NotificationPreferences
	err    error
}

func newFakePreferencesRepository() *fakePreferencesRepository {
	return &fakePreferencesRepository{stored: map[string]domain.NotificationPreferences{}}
}

func (r *fakePreferencesRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakePreferencesRepository) FindPreferences(_ context.Context, tenantID string) (domain.NotificationPreferences, bool, error) {
	preferences, found := r.stored[tenantID]
	return preferences, found, r.err
}

func (r *fakePreferencesRepository) SavePreferences(_ context.Context, preferences domain.NotificationPreferences) error {
	r.stored[preferences.TenantID] = preferences
	return r.err
}

func preferencesUseCase() (*NotificationPreferencesUseCase, *fakePreferencesRepository) {
	repository := newFakePreferencesRepository()
	return NewNotificationPreferencesUseCase(repository, fixedNotificationClock{now: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}), repository
}

func TestPreferencesDefaultToEmailOnlyWhenNothingIsSaved(t *testing.T) {
	useCase, _ := preferencesUseCase()

	preferences, err := useCase.Get(context.Background(), "tenant-a")

	if err != nil || !preferences.Email || preferences.WhatsApp || preferences.SMS || preferences.TenantID != "tenant-a" {
		t.Fatalf("expected email on and the rest off, got %+v err=%v", preferences, err)
	}
}

func TestPreferencesAreSavedPerTenant(t *testing.T) {
	useCase, _ := preferencesUseCase()

	if _, err := useCase.Update(context.Background(), UpdateNotificationPreferencesCommand{TenantID: "tenant-a", Email: true, WhatsApp: true}); err != nil {
		t.Fatalf("update: %v", err)
	}

	mine, _ := useCase.Get(context.Background(), "tenant-a")
	other, _ := useCase.Get(context.Background(), "tenant-b")
	if !mine.WhatsApp {
		t.Fatalf("expected the saved choice, got %+v", mine)
	}
	if other.WhatsApp {
		t.Fatalf("another tenant must keep the defaults, got %+v", other)
	}
}

func TestPreferencesNeedAtLeastOneChannel(t *testing.T) {
	useCase, repository := preferencesUseCase()

	_, err := useCase.Update(context.Background(), UpdateNotificationPreferencesCommand{TenantID: "tenant-a"})

	if !errors.Is(err, domain.ErrNoNotificationChannel) || len(repository.stored) != 0 {
		t.Fatalf("expected a refusal that saves nothing, got err=%v stored=%v", err, repository.stored)
	}
}

func TestPreferencesReadFailureIsNotHiddenBehindDefaults(t *testing.T) {
	useCase, repository := preferencesUseCase()
	repository.err = errors.New("db down")

	if _, err := useCase.Get(context.Background(), "tenant-a"); err == nil {
		t.Fatal("expected the error")
	}
}

func TestPreferencesRequireATenant(t *testing.T) {
	useCase, _ := preferencesUseCase()

	if _, err := useCase.Get(context.Background(), " "); !errors.Is(err, ErrTenantIDRequired) {
		t.Fatalf("expected a tenant to be required, got %v", err)
	}
}
