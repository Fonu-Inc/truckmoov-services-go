package application

import (
	"context"
	"strings"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// SMSDeliveryReceipt is what Dotgo posts to the callback once it knows what
// became of a message. ID is the request id the message was sent under.
type SMSDeliveryReceipt struct {
	ID          string
	Status      string
	ErrorReason string
}

// HandleSMSDeliveryReceiptUseCase lands a Dotgo delivery receipt on the message
// it is about: the outbox row, the delivery log and the delivery event.
type HandleSMSDeliveryReceiptUseCase struct {
	outbox         OutboxRepository
	deliveryLog    DeliveryLogRepository
	eventPublisher DeliveryEventPublisher
	clock          Clock
	idGenerator    IDGenerator
}

func NewHandleSMSDeliveryReceiptUseCase(
	outbox OutboxRepository,
	deliveryLog DeliveryLogRepository,
	eventPublisher DeliveryEventPublisher,
	clock Clock,
	idGenerator IDGenerator,
) *HandleSMSDeliveryReceiptUseCase {
	return &HandleSMSDeliveryReceiptUseCase{
		outbox:         outbox,
		deliveryLog:    deliveryLog,
		eventPublisher: eventPublisher,
		clock:          clock,
		idGenerator:    idGenerator,
	}
}

// Execute reports whether the receipt matched a message we sent. A receipt that
// matches nothing, or carries a status this service does not track, is not an
// error: the sender cannot do anything about it and would only retry.
func (u *HandleSMSDeliveryReceiptUseCase) Execute(ctx context.Context, receipt SMSDeliveryReceipt) (bool, error) {
	providerMessageID := strings.TrimSpace(receipt.ID)
	status, tracked := smsReceiptStatus(receipt.Status)
	if providerMessageID == "" || !tracked {
		return false, nil
	}

	failureReason := ""
	if status == domain.StatusFailed {
		failureReason = strings.TrimSpace(receipt.ErrorReason)
		if failureReason == "" {
			failureReason = strings.TrimSpace(receipt.Status)
		}
	}
	now := u.clock.Now()

	matched := false
	message, found := u.outbox.FindByProviderMessageID(providerMessageID)
	if found {
		matched = true
		// A delivered message stays delivered. Receipts can arrive out of order,
		// and a late "sent" must not move a message backwards.
		if message.Status == domain.StatusDelivered && status != domain.StatusDelivered {
			return true, nil
		}
		message = message.MarkDeliveryStatus(status, now)
		if err := u.outbox.Save(message); err != nil {
			return matched, err
		}
		if publishErr := publishWebhookDeliveryEvent(ctx, u.eventPublisher, u.idGenerator, now, message, failureReason); publishErr != nil {
			// The status is saved; the event is a notification of it.
			_ = publishErr
		}
	}

	if u.deliveryLog != nil {
		updated, err := u.deliveryLog.UpdateStatusByProviderMessageID(ctx, providerMessageID, status, failureReason, now)
		if err != nil {
			return matched, err
		}
		matched = matched || updated
	}
	return matched, nil
}

// smsReceiptStatus maps Dotgo's receipt statuses onto the platform's. Anything
// else, such as an interim state, is not tracked.
func smsReceiptStatus(status string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "sent":
		return domain.StatusSent, true
	case "delivered":
		return domain.StatusDelivered, true
	case "undelivered", "failed", "rejected":
		return domain.StatusFailed, true
	default:
		return "", false
	}
}
