package main

import (
	"context"
	"log/slog"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

type platformMessagingWiring struct {
	bus      eventbus.EventBus
	verified func(eventbus.Handler) eventbus.Handler
	log      *slog.Logger
	clock    application.Clock
	ids      application.IDGenerator
}

// wirePlatformMessaging connects the staff inbox: inbound traffic and receipts
// are published for the realtime gateway, and the gateway's signed commands are
// consumed. It returns the reads behind the REST routes.
func wirePlatformMessaging(
	ctx context.Context,
	wiring platformMessagingWiring,
	repository *infrastructure.PostgresWhatsAppConversationRepository,
	text *application.SendWhatsAppTextMessageUseCase,
	webhook *application.HandleWhatsAppWebhookUseCase,
	access application.AccessLookup,
) *application.PlatformInboxQueries {
	publisher := infrastructure.NewPlatformMessagingEventPublisher(wiring.bus, wiring.clock, wiring.ids)
	notifier := application.NewPlatformMessagingNotifier(repository, publisher, wiring.clock, wiring.log)
	webhook.WithPlatformMessaging(notifier)

	commands := application.NewPlatformMessagingCommands(
		application.NewPlatformMessagingSendUseCase(repository, text, notifier, wiring.clock, wiring.ids),
		application.NewPlatformMessagingReadUseCase(repository, notifier, wiring.clock),
		application.NewAccessAuthorizer(access),
		repository,
		notifier,
		wiring.log,
	)
	wiring.subscribe(ctx, application.PlatformSendRequestedSubject, "platform-messaging-send", commands.HandleSend)
	wiring.subscribe(ctx, application.PlatformReadRequestedSubject, "platform-messaging-read", commands.HandleRead)
	return application.NewPlatformInboxQueries(repository, wiring.clock)
}

func (w platformMessagingWiring) subscribe(ctx context.Context, subject, durable string, handler eventbus.Handler) {
	if _, err := w.bus.Subscribe(ctx, subject, durable, w.verified(handler)); err != nil {
		w.log.Error("platform_messaging_subscribe_failed", "subject", subject, "error", err.Error())
	}
}
