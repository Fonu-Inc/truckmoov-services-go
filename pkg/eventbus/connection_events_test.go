package eventbus

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestConnectionEventsLogDisconnectsAndAsyncErrors(t *testing.T) {
	var out bytes.Buffer
	opts := nats.GetDefaultOptions()
	for _, option := range connectionEvents(slog.New(slog.NewTextHandler(&out, nil))) {
		_ = option(&opts)
	}

	opts.DisconnectedErrCB(nil, errors.New("connection reset"))
	opts.AsyncErrorCB(nil, nil, nats.ErrAuthorization)

	logged := out.String()
	for _, want := range []string{"nats_disconnected", "connection reset", "nats_async_error", "authorization violation"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log missing %q:\n%s", want, logged)
		}
	}
}
