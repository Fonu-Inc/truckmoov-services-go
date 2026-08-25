package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type recordingDeliveryLog struct {
	entries  []domain.DeliveryLogEntry
	appended int
}

func (l *recordingDeliveryLog) EnsureSchema(context.Context) error { return nil }

func (l *recordingDeliveryLog) Append(_ context.Context, entry domain.DeliveryLogEntry) error {
	l.appended++
	l.entries = append(l.entries, entry)
	return nil
}

func (l *recordingDeliveryLog) UpdateStatusByProviderMessageID(
	_ context.Context, providerMessageID, status, failureReason string, occurredAt time.Time,
) (bool, error) {
	for i := range l.entries {
		if l.entries[i].ProviderMessageID == providerMessageID {
			l.entries[i].Status = status
			l.entries[i].FailureReason = failureReason
			l.entries[i].UpdatedAt = occurredAt
			return true, nil
		}
	}
	return false, nil
}

func (l *recordingDeliveryLog) List(
	_ context.Context, tenantID string, filters domain.DeliveryLogFilters,
) ([]domain.DeliveryLogEntry, int, error) {
	matching := make([]domain.DeliveryLogEntry, 0, len(l.entries))
	for _, entry := range l.entries {
		if entry.TenantID != tenantID {
			continue
		}
		if filters.NotificationType != "" && entry.NotificationType != filters.NotificationType {
			continue
		}
		matching = append(matching, entry)
	}
	return matching, len(matching), nil
}

func (l *recordingDeliveryLog) DeleteOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func newSendUseCaseWithLog(log *recordingDeliveryLog, provider *fakeMessageProvider) *SendMessageUseCase {
	return NewSendMessageUseCase(
		newFakeOutboxRepository(),
		provider,
		&fakeDeliveryPublisher{},
		log,
		nil,
		fixedNotificationClock{now: time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "1"},
		"tenant-default",
	)
}

func TestDeliveryLogRecordsTheMessageAsSent(t *testing.T) {
	log := &recordingDeliveryLog{}
	useCase := newSendUseCaseWithLog(log, &fakeMessageProvider{status: domain.StatusSent})

	if _, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID:         "tenant-a",
		Channel:          "email",
		Recipient:        "ops@carrier.ng",
		Content:          "Licence expiring\n\nTwo documents expire this week.",
		NotificationType: domain.NotificationTypeComplianceReminder,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if log.appended != 1 {
		t.Fatalf("expected one entry, got %d", log.appended)
	}
	entry := log.entries[0]
	if entry.Subject != "Licence expiring" {
		t.Fatalf("unexpected subject: %q", entry.Subject)
	}
	if entry.Content != "Two documents expire this week." {
		t.Fatalf("unexpected body: %q", entry.Content)
	}
	if entry.NotificationType != domain.NotificationTypeComplianceReminder {
		t.Fatalf("unexpected type: %q", entry.NotificationType)
	}
}

func TestDeliveryLogDefaultsTheNotificationType(t *testing.T) {
	log := &recordingDeliveryLog{}
	useCase := newSendUseCaseWithLog(log, &fakeMessageProvider{status: domain.StatusSent})

	if _, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID:  "tenant-a",
		Channel:   "sms",
		Recipient: "+2348000000000",
		Content:   "Your job was assigned",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if log.entries[0].NotificationType != domain.NotificationTypeGeneric {
		t.Fatalf("expected the generic type, got %q", log.entries[0].NotificationType)
	}
}

func TestDeliveryLogRecordsAFailedSendWithItsReason(t *testing.T) {
	log := &recordingDeliveryLog{}
	useCase := newSendUseCaseWithLog(log, &fakeMessageProvider{err: errors.New("relay refused")})

	if _, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID:  "tenant-a",
		Channel:   "email",
		Recipient: "ops@carrier.ng",
		Content:   "Subject\n\nBody",
	}); err == nil {
		t.Fatal("expected the send error to surface")
	}

	if log.appended != 1 {
		t.Fatalf("a failed send must still be recorded, got %d entries", log.appended)
	}
	if log.entries[0].FailureReason != "relay refused" {
		t.Fatalf("unexpected failure reason: %q", log.entries[0].FailureReason)
	}
}

func TestDeliveryLogAcceptsStateArrivingAfterTheSend(t *testing.T) {
	log := &recordingDeliveryLog{
		entries: []domain.DeliveryLogEntry{{
			ID:                "msg-1",
			TenantID:          "tenant-a",
			ProviderMessageID: "provider-1",
			Status:            domain.StatusSent,
		}},
	}

	updated, err := log.UpdateStatusByProviderMessageID(
		context.Background(), "provider-1", domain.StatusDelivered, "", time.Now(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !updated {
		t.Fatal("expected the entry to be updated")
	}
	if log.entries[0].Status != domain.StatusDelivered {
		t.Fatalf("unexpected status: %q", log.entries[0].Status)
	}
}

func TestDeliveryLogSeparatesTenants(t *testing.T) {
	log := &recordingDeliveryLog{}
	useCase := newSendUseCaseWithLog(log, &fakeMessageProvider{status: domain.StatusSent})

	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if _, err := useCase.Execute(context.Background(), SendMessageCommand{
			TenantID:  tenant,
			Channel:   "sms",
			Recipient: "+2348000000000",
			Content:   "hello",
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	entries, total, err := log.List(context.Background(), "tenant-a", domain.DeliveryLogFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || entries[0].TenantID != "tenant-a" {
		t.Fatalf("expected only tenant-a entries, got %d", total)
	}
}

func TestSendStillWorksWithoutADeliveryLog(t *testing.T) {
	useCase := NewSendMessageUseCase(
		newFakeOutboxRepository(),
		&fakeMessageProvider{status: domain.StatusSent},
		&fakeDeliveryPublisher{},
		nil,
		nil,
		fixedNotificationClock{now: time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)},
		fixedNotificationIDGenerator{id: "1"},
		"tenant-default",
	)

	if _, err := useCase.Execute(context.Background(), SendMessageCommand{
		TenantID:  "tenant-a",
		Channel:   "sms",
		Recipient: "+2348000000000",
		Content:   "hello",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
