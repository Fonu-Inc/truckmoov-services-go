package application

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

type stubAlertSettings struct {
	setting *AlertSetting
	err     error
	calls   int
}

func (s *stubAlertSettings) ForAlertType(context.Context, string) (*AlertSetting, error) {
	s.calls++
	return s.setting, s.err
}

func alertHarness(setting *AlertSetting, err error) (*DispatchAlertUseCase, *fakeMessageProvider, *recordingCharger) {
	provider := &fakeMessageProvider{status: domain.StatusSent}
	charger := &recordingCharger{}
	send := NewSendMessageUseCase(
		newFakeOutboxRepository(),
		provider,
		&fakeDeliveryPublisher{},
		nil,
		charger,
		fixedNotificationClock{now: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "1"},
		"system",
	)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewDispatchAlertUseCase(&stubAlertSettings{setting: setting, err: err}, send, logger), provider, charger
}

func alertEvent(severity string) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{
		TenantID: "system",
		Data: map[string]any{
			"alert_type": "security_alerts",
			"severity":   severity,
			"subject":    "Suspicious sign-in",
			"body":       "Ten failed attempts in a minute.",
		},
	}
}

func enabled() *AlertSetting {
	return &AlertSetting{
		AlertType:   "security_alerts",
		Enabled:     true,
		Channels:    []string{"email"},
		Recipients:  []string{"sec@truckmoov.com"},
		MinSeverity: "high",
	}
}

func TestAlertReachesEveryRecipientOnEveryChannel(t *testing.T) {
	setting := enabled()
	setting.Recipients = []string{"a@truckmoov.com", "b@truckmoov.com"}
	setting.Channels = []string{"email", "sms"}
	useCase, provider, _ := alertHarness(setting, nil)

	if err := useCase.Handle(context.Background(), alertEvent("critical")); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}

	// Two recipients on two channels is four messages, not two.
	if provider.calls != 4 {
		t.Fatalf("expected 4 sends, got %d", provider.calls)
	}
}

func TestAlertBelowMinimumSeverityIsNotSent(t *testing.T) {
	useCase, provider, _ := alertHarness(enabled(), nil)

	if err := useCase.Handle(context.Background(), alertEvent("low")); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("a low alert was sent against a high minimum")
	}
}

func TestDisabledAlertIsNotSent(t *testing.T) {
	setting := enabled()
	setting.Enabled = false
	useCase, provider, _ := alertHarness(setting, nil)

	if err := useCase.Handle(context.Background(), alertEvent("critical")); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("a disabled alert was sent")
	}
}

// An alert with no recipient goes nowhere, and must not look like a success.
func TestAlertWithNoRecipientsIsNotSent(t *testing.T) {
	setting := enabled()
	setting.Recipients = nil
	useCase, provider, _ := alertHarness(setting, nil)

	if err := useCase.Handle(context.Background(), alertEvent("critical")); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("an alert with no recipients produced a send")
	}
}

func TestUnconfiguredAlertTypeIsNotSent(t *testing.T) {
	useCase, provider, _ := alertHarness(nil, nil)

	if err := useCase.Handle(context.Background(), alertEvent("critical")); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("an unconfigured alert produced a send")
	}
}

// Dropping the alert because the settings could not be read would lose exactly
// the events worth hearing about, so it is retried instead.
func TestUnreadableSettingsAreRetriedNotDropped(t *testing.T) {
	useCase, provider, _ := alertHarness(nil, errors.New("auth down"))

	err := useCase.Handle(context.Background(), alertEvent("critical"))

	if err == nil {
		t.Fatalf("expected an error so the event is redelivered")
	}
	if provider.calls != 0 {
		t.Fatalf("sent despite unreadable settings")
	}
}

func TestUnknownSeveritySuppressesRatherThanPages(t *testing.T) {
	useCase, provider, _ := alertHarness(enabled(), nil)

	if err := useCase.Handle(context.Background(), alertEvent("catastrophic")); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("an unrecognised severity paged everyone")
	}
}

func TestAlertIsNotChargedToATenant(t *testing.T) {
	useCase, _, charger := alertHarness(enabled(), nil)

	if err := useCase.Handle(context.Background(), alertEvent("critical")); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}

	// Email is not billable, and a platform alert is the platform's own cost.
	if len(charger.charges) != 0 {
		t.Fatalf("a platform alert was charged to a tenant: %+v", charger.charges)
	}
}

func TestMalformedAlertIsIgnoredNotRetried(t *testing.T) {
	useCase, provider, _ := alertHarness(enabled(), nil)

	err := useCase.Handle(context.Background(), eventbus.EventEnvelope{
		TenantID: "system",
		Data:     map[string]any{"severity": "critical"},
	})

	if err != nil {
		t.Fatalf("a malformed alert should not be retried for ever: %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("sent a malformed alert")
	}
}
