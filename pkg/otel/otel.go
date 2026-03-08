package otel

import (
	"context"
	"log/slog"
)

func Setup(_ context.Context, service string, log *slog.Logger) (func(context.Context) error, error) {
	log.Info("otel_setup_placeholder", "service", service)
	return func(context.Context) error { return nil }, nil
}
