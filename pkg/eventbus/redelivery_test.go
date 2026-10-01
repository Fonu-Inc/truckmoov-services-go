package eventbus

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type fakeFailedDelivery struct {
	delivered uint64
	delay     time.Duration
	naked     bool
	termed    bool
}

func (f *fakeFailedDelivery) Metadata() (*nats.MsgMetadata, error) {
	return &nats.MsgMetadata{NumDelivered: f.delivered}, nil
}

func (f *fakeFailedDelivery) NakWithDelay(delay time.Duration, _ ...nats.AckOpt) error {
	f.naked, f.delay = true, delay
	return nil
}

func (f *fakeFailedDelivery) Term(_ ...nats.AckOpt) error {
	f.termed = true
	return nil
}

func TestFailedDeliveryBacksOffInsteadOfRetryingImmediately(t *testing.T) {
	msg := &fakeFailedDelivery{delivered: 1}
	retryOrTerminate(msg, slog.New(slog.NewTextHandler(io.Discard, nil)), "tm.system.x")
	if !msg.naked || msg.delay <= 0 {
		t.Fatalf("expected delayed nak, got naked=%v delay=%v", msg.naked, msg.delay)
	}
}

func TestFailedDeliveryIsTerminatedAfterMaxDeliveries(t *testing.T) {
	msg := &fakeFailedDelivery{delivered: maxDeliveries}
	retryOrTerminate(msg, slog.New(slog.NewTextHandler(io.Discard, nil)), "tm.system.x")
	if !msg.termed || msg.naked {
		t.Fatalf("expected term, got termed=%v naked=%v", msg.termed, msg.naked)
	}
}
