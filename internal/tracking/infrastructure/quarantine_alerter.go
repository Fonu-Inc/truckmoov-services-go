package infrastructure

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/identity"
)

const quarantineAlertSubject = "tm." + identity.SystemTenantID + ".alert.raised"

// QuarantineAlerter raises a platform security alert for a noisy unknown IMEI.
type QuarantineAlerter struct {
	bus eventbus.EventBus
	ids TimeIDGenerator
	log *slog.Logger
}

func NewQuarantineAlerter(bus eventbus.EventBus, log *slog.Logger) *QuarantineAlerter {
	return &QuarantineAlerter{bus: bus, log: log}
}

func (a *QuarantineAlerter) NoisyIMEI(ctx context.Context, imei string, count int) {
	err := a.bus.Publish(ctx, quarantineAlertSubject, eventbus.EventEnvelope{
		ID:         a.ids.New("evt"),
		Type:       "alert.raised",
		Version:    1,
		OccurredAt: time.Now().UTC(),
		TenantID:   identity.SystemTenantID,
		Data: map[string]any{
			"alert_type": "security_alerts",
			"severity":   "high",
			"subject":    "Unrecognised device flooding ingest",
			"body": fmt.Sprintf(
				"A device with IMEI %s that is not registered to any customer sent more than %d messages in the last hour. Dismiss or block it in the admin Devices page.",
				imei, count),
			"context": map[string]any{"imei": imei, "messages_in_hour": count},
		},
	})
	if err != nil {
		a.log.Warn("quarantine_alert_failed", "imei", imei, "error", err.Error())
	}
}
