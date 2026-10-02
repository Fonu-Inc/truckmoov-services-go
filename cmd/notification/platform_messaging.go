package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/infrastructure"
	notificationhttp "github.com/truckmoov/services-go/internal/notification/interfaces/http"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

const scheduledSendInterval = 20 * time.Second

type platformMessagingWiring struct {
	bus      eventbus.EventBus
	verified func(eventbus.Handler) eventbus.Handler
	log      *slog.Logger
	clock    application.Clock
	ids      application.IDGenerator
}

type platformInboxExtras struct {
	notifier     *application.PlatformMessagingNotifier
	media        application.PlatformMediaProvider
	templateSend *application.PlatformTemplateSendUseCase
	drivers      application.DriverLookup
	campaigns    *application.PlatformCampaigns
	runner       *application.CampaignRunner
	sms          *application.PlatformSMSTemplates
	voice        *application.PlatformVoice
}

type platformInbox struct {
	queries *application.PlatformInboxQueries
	tools   *notificationhttp.PlatformInboxTools
}

// wirePlatformMessaging connects the staff inbox: inbound traffic and receipts
// are published for the realtime gateway, and the gateway's signed commands are
// consumed. It returns the reads and the staff actions behind the REST routes.
func wirePlatformMessaging(
	ctx context.Context,
	wiring platformMessagingWiring,
	repository *infrastructure.PostgresWhatsAppConversationRepository,
	text *application.SendWhatsAppTextMessageUseCase,
	webhook *application.HandleWhatsAppWebhookUseCase,
	access application.AccessLookup,
	extras platformInboxExtras,
) platformInbox {
	notifier := extras.notifier
	webhook.WithPlatformMessaging(notifier)

	send := application.NewPlatformMessagingSendUseCase(repository, text, notifier, wiring.clock, wiring.ids)
	commands := application.NewPlatformMessagingCommands(
		send,
		application.NewPlatformMessagingReadUseCase(repository, notifier, wiring.clock),
		application.NewAccessAuthorizer(access),
		repository,
		notifier,
		wiring.log,
	)
	wiring.subscribe(ctx, application.PlatformSendRequestedSubject, "platform-messaging-send", commands.HandleSend)
	wiring.subscribe(ctx, application.PlatformReadRequestedSubject, "platform-messaging-read", commands.HandleRead)

	media := application.NewPlatformMediaSendUseCase(repository, extras.media, notifier, wiring.clock, wiring.ids)
	scheduled := application.NewPlatformScheduledSends(send, repository)
	go runScheduledSends(ctx, scheduled, extras.runner, wiring.log)
	return platformInbox{
		queries: application.NewPlatformInboxQueries(repository, wiring.clock),
		tools: &notificationhttp.PlatformInboxTools{
			Admin:     application.NewPlatformInboxAdmin(repository, notifier, wiring.clock, wiring.ids),
			Media:     media,
			MediaGet:  application.NewPlatformMediaFetchUseCase(repository, extras.media),
			Retry:     application.NewPlatformRetryUseCase(repository, send, media, extras.templateSend, notifier),
			Scheduled: scheduled,
			Canned:    application.NewCannedReplies(repository, wiring.clock, wiring.ids),
			Links:     application.NewPlatformContactLinks(repository, extras.drivers),
			Campaigns: extras.campaigns,
			SMS:       extras.sms,
			Voice:     extras.voice,
		},
	}
}

func runScheduledSends(ctx context.Context, scheduled *application.PlatformScheduledSends, campaigns *application.CampaignRunner, log *slog.Logger) {
	ticker := time.NewTicker(scheduledSendInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := scheduled.RunDue(ctx); err != nil {
				log.Warn("platform_scheduled_send_failed", "error", err.Error())
			}
			if _, err := campaigns.RunDue(ctx); err != nil {
				log.Warn("platform_scheduled_campaign_failed", "error", err.Error())
			}
			if _, err := campaigns.RetryInterrupted(ctx); err != nil {
				log.Warn("campaign_interrupted_retry_failed", "error", err.Error())
			}
		}
	}
}

func (w platformMessagingWiring) subscribe(ctx context.Context, subject, durable string, handler eventbus.Handler) {
	if _, err := w.bus.Subscribe(ctx, subject, durable, w.verified(handler)); err != nil {
		w.log.Error("platform_messaging_subscribe_failed", "subject", subject, "error", err.Error())
	}
}
