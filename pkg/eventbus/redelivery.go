package eventbus

import (
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

const maxDeliveries = 8

var redeliveryBackoff = []time.Duration{
	time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute,
	5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
}

type failedDelivery interface {
	Metadata() (*nats.MsgMetadata, error)
	NakWithDelay(delay time.Duration, opts ...nats.AckOpt) error
	Term(opts ...nats.AckOpt) error
}

func redeliveryDelay(delivered uint64) time.Duration {
	index := int(delivered) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(redeliveryBackoff) {
		index = len(redeliveryBackoff) - 1
	}
	return redeliveryBackoff[index]
}

// retryOrTerminate backs off between redeliveries and gives up after
// maxDeliveries, dead-lettering the event first (kept for retry if that
// fails), so one event that can never succeed does not loop hot forever.
func retryOrTerminate(msg failedDelivery, log *slog.Logger, subject string, deadLetter func(attempts uint64) error) {
	meta, err := msg.Metadata()
	if err != nil {
		_ = msg.NakWithDelay(redeliveryBackoff[0])
		return
	}
	if meta.NumDelivered >= maxDeliveries {
		if deadLetter != nil {
			if err := deadLetter(meta.NumDelivered); err != nil {
				log.Error("event_dead_letter_failed", "subject", subject, "deliveries", meta.NumDelivered, "error", err.Error())
				_ = msg.NakWithDelay(redeliveryBackoff[len(redeliveryBackoff)-1])
				return
			}
		}
		log.Error("event_abandoned", "subject", subject, "deliveries", meta.NumDelivered)
		_ = msg.Term()
		return
	}
	_ = msg.NakWithDelay(redeliveryDelay(meta.NumDelivered))
}
