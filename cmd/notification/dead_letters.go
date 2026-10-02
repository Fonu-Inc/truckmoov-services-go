package main

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

const (
	deadLetterConsumer      = "dead-letters"
	deadLetterPurgeInterval = time.Hour
)

type deadLetterWiring struct {
	bus      eventbus.EventBus
	verified func(eventbus.Handler) eventbus.Handler
	log      *slog.Logger
	db       *sql.DB
	claims   application.EventClaimStore
	clock    application.Clock
	ids      application.IDGenerator
}

// wireDeadLetters stores every event the bus gave up on, so staff can see and
// replay them past the stream's seven day retention. It returns nil when the
// table cannot be created, which turns the staff routes off rather than
// failing the service.
func wireDeadLetters(ctx context.Context, wiring deadLetterWiring) *application.DeadLetters {
	repository := infrastructure.NewPostgresDeadLetterRepository(wiring.db)
	if err := repository.EnsureSchema(ctx); err != nil {
		wiring.log.Error("dead_letter_schema_failed", "error", err)
		return nil
	}
	deadLetters := application.NewDeadLetters(
		repository,
		wiring.bus,
		infrastructure.NewPlatformAlertPublisher(wiring.bus, wiring.clock, wiring.ids),
		wiring.claims,
		wiring.clock,
		wiring.log,
	)
	if _, err := wiring.bus.Subscribe(ctx, application.DeadLetterSubject, deadLetterConsumer, wiring.verified(deadLetters.Handle)); err != nil {
		wiring.log.Error("dead_letter_subscribe_failed", "error", err.Error())
	}
	go runDeadLetterPurge(ctx, deadLetters, wiring.log)
	return deadLetters
}

func runDeadLetterPurge(ctx context.Context, deadLetters *application.DeadLetters, log *slog.Logger) {
	ticker := time.NewTicker(deadLetterPurgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := deadLetters.Purge(ctx); err != nil {
				log.Warn("dead_letter_purge_failed", "error", err.Error())
			}
		}
	}
}
