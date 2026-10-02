package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
)

const (
	whatsAppSweepInterval = 24 * time.Hour
	whatsAppSweepDelay    = time.Minute
)

// runWhatsAppTokenSweep re-checks every connected account once a day, so a
// token that died quietly shows as needing a reconnect before staff send.
func runWhatsAppTokenSweep(ctx context.Context, connector *application.WhatsAppConnector, log *slog.Logger) {
	timer := time.NewTimer(whatsAppSweepDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			checked, err := connector.CheckAll(ctx)
			if err != nil {
				log.Warn("whatsapp_account_sweep_failed", "checked", checked, "error", err.Error())
			}
			timer.Reset(whatsAppSweepInterval)
		}
	}
}
