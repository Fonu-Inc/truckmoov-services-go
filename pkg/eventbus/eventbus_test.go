package eventbus

import (
	"testing"

	"github.com/nats-io/nats.go"
)

func TestBuildStreamConfig(t *testing.T) {
	cfg := buildStreamConfig("truckmoov_events")

	if cfg.Name != "truckmoov_events" {
		t.Fatalf("expected name truckmoov_events, got %q", cfg.Name)
	}
	if len(cfg.Subjects) != 1 || cfg.Subjects[0] != "tm.*.>" {
		t.Fatalf("expected subjects [tm.*.>], got %v", cfg.Subjects)
	}
	if cfg.Storage != nats.FileStorage {
		t.Fatalf("expected FileStorage, got %v", cfg.Storage)
	}
	if cfg.Retention != nats.LimitsPolicy {
		t.Fatalf("expected LimitsPolicy, got %v", cfg.Retention)
	}
	if cfg.Discard != nats.DiscardOld {
		t.Fatalf("expected DiscardOld, got %v", cfg.Discard)
	}
	// Replicas is intentionally left at the zero value (NATS default: 1)
	// until cluster health is confirmed post-deploy — see the comment on
	// buildStreamConfig. This assertion is a deliberate regression guard:
	// bumping it without that verification can crash-loop every Go service.
	if cfg.Replicas != 0 {
		t.Fatalf("expected Replicas to be left unset (0, NATS default 1) until cluster health is verified, got %d", cfg.Replicas)
	}
}

func TestSanitizeJetStreamName(t *testing.T) {
	cases := map[string]string{
		"truckmoov_events":  "truckmoov_events",
		"truckmoov events!": "truckmoov_events",
		"---":               "---",
		"":                  "truckmoov_events",
		"a__b":              "a_b",
	}
	for input, want := range cases {
		if got := sanitizeJetStreamName(input); got != want {
			t.Errorf("sanitizeJetStreamName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIngestPublishWaitOption(t *testing.T) {
	for _, mode := range []IngestPublishWait{IngestPublishWaitAck, IngestPublishWaitFlush, IngestPublishWaitNone} {
		bus := &natsEventBus{ingestPublishWait: IngestPublishWaitFlush}
		WithIngestPublishWait(mode)(bus)
		if bus.ingestPublishWait != mode {
			t.Errorf("WithIngestPublishWait(%q) = %q", mode, bus.ingestPublishWait)
		}
	}

	bus := &natsEventBus{ingestPublishWait: IngestPublishWaitFlush}
	WithIngestPublishWait("not-a-mode")(bus)
	if bus.ingestPublishWait != IngestPublishWaitFlush {
		t.Errorf("invalid mode must keep flush, got %q", bus.ingestPublishWait)
	}
}
