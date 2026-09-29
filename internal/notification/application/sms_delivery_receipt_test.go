package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// trackedProvider is a provider that names the message it sent, as Dotgo's does.
type trackedProvider struct{ id string }

func (p trackedProvider) Send(context.Context, string, string, string, string) (string, error) {
	return domain.StatusSent, nil
}

func (p trackedProvider) SendTracked(context.Context, string, string, string, string) (domain.MessageSendResult, error) {
	return domain.MessageSendResult{Status: domain.StatusSent, ProviderMessageID: p.id}, nil
}

type smsReceiptFixture struct {
	outbox    *fakeOutboxRepository
	log       *recordingDeliveryLog
	publisher *fakeDeliveryPublisher
	send      *SendMessageUseCase
	receipts  *HandleSMSDeliveryReceiptUseCase
}

func newSMSReceiptFixture(providerID string) smsReceiptFixture {
	outbox := newFakeOutboxRepository()
	log := &recordingDeliveryLog{}
	publisher := &fakeDeliveryPublisher{}
	clock := fixedNotificationClock{now: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)}
	ids := fixedNotificationIDGenerator{id: "1"}
	return smsReceiptFixture{
		outbox:    outbox,
		log:       log,
		publisher: publisher,
		send: NewSendMessageUseCase(
			outbox, trackedProvider{id: providerID}, publisher, log, nil, clock, ids, "tenant-default",
		),
		receipts: NewHandleSMSDeliveryReceiptUseCase(outbox, log, publisher, clock, ids),
	}
}

func (f smsReceiptFixture) sendSMS(t *testing.T) domain.OutboxMessage {
	t.Helper()
	message, err := f.send.Execute(context.Background(), SendMessageCommand{
		TenantID:  "tenant-a",
		Channel:   "sms",
		Recipient: "+2348012345678",
		Content:   "Your job is assigned",
	})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	return message
}

func TestSendKeepsTheProviderRequestID(t *testing.T) {
	f := newSMSReceiptFixture("482910375561")

	message := f.sendSMS(t)

	if message.ProviderMessageID != "482910375561" {
		t.Fatalf("expected the outbox row to hold the request id, got %q", message.ProviderMessageID)
	}
	if len(f.log.entries) != 1 || f.log.entries[0].ProviderMessageID != "482910375561" {
		t.Fatalf("expected the delivery log to hold the request id, got %+v", f.log.entries)
	}
}

func TestReceiptMarksTheMessageDelivered(t *testing.T) {
	f := newSMSReceiptFixture("111")
	message := f.sendSMS(t)
	publishedBefore := f.publisher.published

	matched, err := f.receipts.Execute(context.Background(), SMSDeliveryReceipt{ID: "111", Status: "delivered"})
	if err != nil || !matched {
		t.Fatalf("expected a match, got matched=%v err=%v", matched, err)
	}

	stored, _ := f.outbox.FindByID(message.ID)
	if stored.Status != domain.StatusDelivered {
		t.Fatalf("expected the outbox row delivered, got %s", stored.Status)
	}
	if f.log.entries[0].Status != domain.StatusDelivered {
		t.Fatalf("expected the delivery log delivered, got %s", f.log.entries[0].Status)
	}
	if f.publisher.published != publishedBefore+1 {
		t.Fatalf("expected one delivery event for the receipt")
	}
}

func TestReceiptRecordsWhyAMessageFailed(t *testing.T) {
	for _, status := range []string{"failed", "undelivered", "rejected"} {
		f := newSMSReceiptFixture("222")
		message := f.sendSMS(t)

		matched, err := f.receipts.Execute(context.Background(), SMSDeliveryReceipt{ID: "222", Status: status, ErrorReason: "handset unreachable"})
		if err != nil || !matched {
			t.Fatalf("%s: expected a match, got matched=%v err=%v", status, matched, err)
		}
		stored, _ := f.outbox.FindByID(message.ID)
		if stored.Status != domain.StatusFailed {
			t.Fatalf("%s: expected failed, got %s", status, stored.Status)
		}
		if f.log.entries[0].FailureReason != "handset unreachable" {
			t.Fatalf("%s: expected the reason in the log, got %q", status, f.log.entries[0].FailureReason)
		}
	}
}

func TestLateReceiptDoesNotMoveADeliveredMessageBackwards(t *testing.T) {
	f := newSMSReceiptFixture("333")
	message := f.sendSMS(t)
	_, _ = f.receipts.Execute(context.Background(), SMSDeliveryReceipt{ID: "333", Status: "delivered"})

	matched, err := f.receipts.Execute(context.Background(), SMSDeliveryReceipt{ID: "333", Status: "sent"})
	if err != nil || !matched {
		t.Fatalf("expected the receipt to match, got matched=%v err=%v", matched, err)
	}

	stored, _ := f.outbox.FindByID(message.ID)
	if stored.Status != domain.StatusDelivered {
		t.Fatalf("expected the message to stay delivered, got %s", stored.Status)
	}
	if f.log.entries[0].Status != domain.StatusDelivered {
		t.Fatalf("expected the log to stay delivered, got %s", f.log.entries[0].Status)
	}
}

func TestReceiptForAMessageWeNeverSentMatchesNothing(t *testing.T) {
	f := newSMSReceiptFixture("444")
	f.sendSMS(t)

	matched, err := f.receipts.Execute(context.Background(), SMSDeliveryReceipt{ID: "999999999999", Status: "delivered"})

	if err != nil || matched {
		t.Fatalf("expected no match and no error, got matched=%v err=%v", matched, err)
	}
	if f.log.entries[0].Status == domain.StatusDelivered {
		t.Fatal("a receipt for another id must not touch this message")
	}
}

func TestReceiptWithoutAnIDOrWithAnInterimStatusIsIgnored(t *testing.T) {
	f := newSMSReceiptFixture("555")
	f.sendSMS(t)

	for _, receipt := range []SMSDeliveryReceipt{
		{ID: "", Status: "delivered"},
		{ID: "555", Status: "queued"},
		{ID: "555", Status: ""},
	} {
		matched, err := f.receipts.Execute(context.Background(), receipt)
		if err != nil || matched {
			t.Fatalf("%+v: expected it to be ignored, got matched=%v err=%v", receipt, matched, err)
		}
	}
	if f.log.entries[0].Status != domain.StatusSent {
		t.Fatalf("expected the message untouched, got %s", f.log.entries[0].Status)
	}
}

func TestProviderWithoutAnIDStillSends(t *testing.T) {
	outbox := newFakeOutboxRepository()
	useCase := NewSendMessageUseCase(
		outbox, &fakeMessageProvider{status: domain.StatusSent}, &fakeDeliveryPublisher{}, nil, nil,
		fixedNotificationClock{now: time.Now()}, fixedNotificationIDGenerator{id: "1"}, "tenant-default",
	)

	message, err := useCase.Execute(context.Background(), SendMessageCommand{Channel: "email", Recipient: "a@b.co", Content: "hi"})

	if err != nil || message.Status != domain.StatusSent || message.ProviderMessageID != "" {
		t.Fatalf("expected a plain send with no id, got %+v err=%v", message, err)
	}
}
