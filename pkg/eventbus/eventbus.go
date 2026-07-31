package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
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

var invalidJetStreamNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
var repeatedUnderscores = regexp.MustCompile(`_+`)
var leadingOrTrailingUnderscores = regexp.MustCompile(`^_+|_+$`)

func sanitizeJetStreamName(streamName string) string {
	normalized := invalidJetStreamNameChars.ReplaceAllString(streamName, "_")
	normalized = repeatedUnderscores.ReplaceAllString(normalized, "_")
	normalized = leadingOrTrailingUnderscores.ReplaceAllString(normalized, "")
	if normalized == "" {
		return "truckmoov_events"
	}
	return normalized
}

// buildStreamConfig is factored out of New so its fields (retention, bounds,
// replication) can be unit tested without a live NATS connection.
//
// Replicas is intentionally left unset (NATS default: 1). The NATS
// StatefulSet (see truckmoov-gitops/manifests/platform/base/nats-statefulset.yaml)
// currently runs as a 3-node cluster capable of R3, but the stream itself has
// not yet been bumped to Replicas:3 pending manual confirmation that the
// cluster has actually formed a healthy quorum post-deploy — requesting R3
// against a cluster that hasn't finished forming would fail stream
// creation/update, and that error is fatal at service startup (see
// cmd/*/main.go), taking down every Go service at once. Bump this only after
// verifying cluster health (e.g. `nats server list` or the /varz endpoint on
// port 8222 across all three pods shows a formed cluster).
func buildStreamConfig(streamName string) *nats.StreamConfig {
	// Bounded retention so a high-rate stream can never fill the data PVC and
	// stall JetStream. Discard oldest once limits are hit.
	return &nats.StreamConfig{
		Name:              streamName,
		Subjects:          []string{"tm.*.>"},
		Storage:           nats.FileStorage,
		Retention:         nats.LimitsPolicy,
		Discard:           nats.DiscardOld,
		MaxAge:            7 * 24 * time.Hour,
		MaxBytes:          1 << 30, // 1 GiB
		MaxMsgsPerSubject: 100_000,
	}
}

// New connects to NATS JetStream and ensures the event stream exists with
// bounded retention. It fails loudly (returns an error) rather than silently
// dropping events through a no-op bus — callers should treat a nil error as a
// hard requirement and exit on failure.
func New(ctx context.Context, natsURL, streamName string, log *slog.Logger) (EventBus, error) {
	effectiveStreamName := sanitizeJetStreamName(streamName)
	if effectiveStreamName != streamName {
		log.Warn("nats_stream_name_sanitized", "configured_stream", streamName, "effective_stream", effectiveStreamName)
	}

	nc, err := nats.Connect(natsURL, nats.Name("truckmoov-eventbus"))
	if err != nil {
		return nil, fmt.Errorf("connect nats %q: %w", natsURL, err)
	}

	js, err := nc.JetStream()
	if err != nil {
		_ = nc.Drain()
		return nil, fmt.Errorf("init jetstream: %w", err)
	}

	streamCfg := buildStreamConfig(effectiveStreamName)
	if _, infoErr := js.StreamInfo(effectiveStreamName); infoErr != nil {
		if _, err = js.AddStream(streamCfg); err != nil {
			_ = nc.Drain()
			return nil, fmt.Errorf("add stream %q: %w", effectiveStreamName, err)
		}
	} else if _, err = js.UpdateStream(streamCfg); err != nil {
		_ = nc.Drain()
		return nil, fmt.Errorf("update stream %q: %w", effectiveStreamName, err)
	}

	_ = ctx
	log.Info("nats_eventbus_ready", "url", natsURL, "stream", effectiveStreamName)
	return &natsEventBus{log: log, nc: nc, js: js, streamName: effectiveStreamName}, nil
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
