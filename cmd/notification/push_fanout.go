package main

import (
	"context"
	"log/slog"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

func wirePushFanout(ctx context.Context, bus eventbus.EventBus, verified func(eventbus.Handler) eventbus.Handler, log *slog.Logger, fanout *application.PushFanout) {
	subscriptions := []struct {
		subject, durable string
		handler          eventbus.Handler
	}{
		{"tm.*.alert.triggered", "push-alert-triggered", fanout.HandleAlert},
		{"tm.*.job.status.changed", "push-job-status-changed", fanout.HandleJobStatus},
		{"tm.*.job.offer.created", "push-job-offer-created", fanout.HandleJobOffer},
		{"tm.*.job.offer.responded", "push-job-offer-responded", fanout.HandleJobOffer},
		{"tm.*.activity.logged", "push-activity-logged", fanout.HandleActivity},
	}
	for _, subscription := range subscriptions {
		if _, err := bus.Subscribe(ctx, subscription.subject, subscription.durable, verified(subscription.handler)); err != nil {
			log.Error("push_subscribe_failed", "subject", subscription.subject, "error", err.Error())
		}
	}
}
