package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

type EventEnvelope struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Version       int            `json:"version"`
	OccurredAt    time.Time      `json:"occurred_at"`
	TenantID      string         `json:"tenant_id"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Actor         map[string]any `json:"actor,omitempty"`
	Data          map[string]any `json:"data"`
}

type Handler func(ctx context.Context, envelope EventEnvelope) error

type Subscription interface {
	Unsubscribe() error
}

type EventBus interface {
	Publish(ctx context.Context, subject string, envelope EventEnvelope) error
	Subscribe(ctx context.Context, subject, durable string, handler Handler) (Subscription, error)
	Close() error
}

type natsEventBus struct {
	log        *slog.Logger
	nc         *nats.Conn
	js         nats.JetStreamContext
	streamName string
}

type noOpEventBus struct {
	log *slog.Logger
}

func New(ctx context.Context, natsURL, streamName string, log *slog.Logger) EventBus {
	nc, err := nats.Connect(natsURL, nats.Name("truckmoov-eventbus"))
	if err != nil {
		log.Warn("nats_connect_failed_using_noop", "error", err.Error())
		return &noOpEventBus{log: log}
	}

	js, err := nc.JetStream()
	if err != nil {
		log.Warn("nats_jetstream_failed_using_noop", "error", err.Error())
		_ = nc.Drain()
		return &noOpEventBus{log: log}
	}

	if _, err = js.StreamInfo(streamName); err != nil {
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     streamName,
			Subjects: []string{"tm.*.>"},
			Storage:  nats.FileStorage,
		})
		if err != nil {
			log.Warn("nats_stream_setup_failed_using_noop", "error", err.Error())
			_ = nc.Drain()
			return &noOpEventBus{log: log}
		}
	}

	_ = ctx
	log.Info("nats_eventbus_ready", "url", natsURL, "stream", streamName)
	return &natsEventBus{log: log, nc: nc, js: js, streamName: streamName}
}

func (b *natsEventBus) Publish(_ context.Context, subject string, envelope EventEnvelope) error {
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	_, err = b.js.Publish(subject, payload)
	if err != nil {
		return fmt.Errorf("publish event: %w", err)
	}

	b.log.Info("event_published", "subject", subject, "type", envelope.Type, "tenant_id", envelope.TenantID)
	return nil
}

func (b *natsEventBus) Subscribe(_ context.Context, subject, durable string, handler Handler) (Subscription, error) {
	sub, err := b.js.QueueSubscribe(
		subject,
		durable,
		func(msg *nats.Msg) {
			var envelope EventEnvelope
			if unmarshalErr := json.Unmarshal(msg.Data, &envelope); unmarshalErr != nil {
				b.log.Warn("event_unmarshal_failed", "error", unmarshalErr.Error())
				_ = msg.Ack()
				return
			}

			if handleErr := handler(context.Background(), envelope); handleErr != nil {
				b.log.Warn("event_handler_failed", "error", handleErr.Error(), "subject", subject)
				_ = msg.Nak()
				return
			}
			_ = msg.Ack()
		},
		nats.Durable(durable),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.DeliverNew(),
	)
	if err != nil {
		return nil, fmt.Errorf("subscribe to %s: %w", subject, err)
	}

	b.log.Info("event_subscription_ready", "subject", subject, "durable", durable)
	return sub, nil
}

func (b *natsEventBus) Close() error {
	if b.nc == nil {
		return nil
	}
	return b.nc.Drain()
}

func (b *noOpEventBus) Publish(_ context.Context, subject string, envelope EventEnvelope) error {
	b.log.Info("noop_publish", "subject", subject, "type", envelope.Type)
	return nil
}

func (b *noOpEventBus) Subscribe(_ context.Context, subject, durable string, _ Handler) (Subscription, error) {
	b.log.Info("noop_subscribe", "subject", subject, "durable", durable)
	return &noopSubscription{}, nil
}

func (b *noOpEventBus) Close() error {
	return nil
}

type noopSubscription struct{}

func (n *noopSubscription) Unsubscribe() error {
	return nil
}
