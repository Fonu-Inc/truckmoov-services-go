package eventbus

import (
	"errors"
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
	retryOrTerminate(msg, slog.New(slog.NewTextHandler(io.Discard, nil)), "tm.system.x", nil)
	if !msg.naked || msg.delay <= 0 {
		t.Fatalf("expected delayed nak, got naked=%v delay=%v", msg.naked, msg.delay)
	}
}

func TestFailedDeliveryIsTerminatedAfterMaxDeliveries(t *testing.T) {
	msg := &fakeFailedDelivery{delivered: maxDeliveries}
	retryOrTerminate(msg, slog.New(slog.NewTextHandler(io.Discard, nil)), "tm.system.x", nil)
	if !msg.termed || msg.naked {
		t.Fatalf("expected term, got termed=%v naked=%v", msg.termed, msg.naked)
	}
}

func TestAbandonedEventIsDeadLetteredBeforeTerm(t *testing.T) {
	msg := &fakeFailedDelivery{delivered: maxDeliveries}
	var got uint64
	retryOrTerminate(msg, slog.New(slog.NewTextHandler(io.Discard, nil)), "tm.t1.x", func(attempts uint64) error { got = attempts; return nil })
	if got != maxDeliveries || !msg.termed {
		t.Fatalf("expected dead letter with %d attempts then term, got %d termed=%v", maxDeliveries, got, msg.termed)
	}
}

func TestEventIsKeptWhenDeadLetterFails(t *testing.T) {
	msg := &fakeFailedDelivery{delivered: maxDeliveries}
	retryOrTerminate(msg, slog.New(slog.NewTextHandler(io.Discard, nil)), "tm.t1.x", func(uint64) error { return errors.New("down") })
	if msg.termed || !msg.naked {
		t.Fatalf("expected nak without term, got termed=%v naked=%v", msg.termed, msg.naked)
	}
}

func TestDeadLetterSubjectKeepsEventPartAndRefusesLoops(t *testing.T) {
	if got, ok := deadLetterSubject("tm.t1.job.created"); !ok || got != "tm.system.dlq.job.created" {
		t.Fatalf("got %q %v", got, ok)
	}
	if _, ok := deadLetterSubject("tm.system.dlq.job.created"); ok {
		t.Fatal("dead letters must not be dead-lettered")
	}
}

func TestDeadLetterEnvelopeWrapsTheOriginalAndVerifies(t *testing.T) {
	original := EventEnvelope{ID: "e1", Type: "job.created", TenantID: "t1"}
	env := deadLetterEnvelope(original, "tm.t1.job.created", 8, errors.New("boom"), time.Unix(0, 0))
	if env.TenantID != "system" || env.Data["original_subject"] != "tm.t1.job.created" || env.Data["attempts"] != uint64(8) || env.Data["last_error"] != "boom" || env.Data["failed_at"] == "" {
		t.Fatalf("unexpected envelope %+v", env.Data)
	}
	if _, err := BusSignature("s", "tm.system.dlq.job.created", env); err != nil {
		t.Fatalf("dead letter must be signable: %v", err)
	}
}
