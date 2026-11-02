package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New builds the service logger. The default stays Info; LOG_LEVEL=debug turns
// per-request access logs back on for local debugging (they are Debug-level so
// a 1k VU flood cannot fill stdout/Loki and block the request path).
func New(service string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(handler).With("service", service)
}
