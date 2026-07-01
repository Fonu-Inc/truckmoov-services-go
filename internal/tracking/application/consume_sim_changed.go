package application

import (
	"context"
	"log/slog"
	"strings"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

// ConsumeSIMChangedUseCase keeps the device→SIM resolver cache in sync with auth's
// SIM events (subject tm.*.sim.changed), keyed by the assigned device id.
type ConsumeSIMChangedUseCase struct {
	resolver SIMResolver
	log      *slog.Logger
}

func NewConsumeSIMChangedUseCase(resolver SIMResolver, log *slog.Logger) *ConsumeSIMChangedUseCase {
	return &ConsumeSIMChangedUseCase{resolver: resolver, log: log}
}

func (u *ConsumeSIMChangedUseCase) Handle(_ context.Context, envelope eventbus.EventEnvelope) error {
	deviceID, _ := envelope.Data["device_id"].(string)
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		u.log.Warn("sim_changed_missing_device_id", "tenant", envelope.TenantID)
		return nil
	}

	action, _ := envelope.Data["action"].(string)
	switch action {
	case "unassigned", "deleted":
		return u.resolver.Evict(deviceID)
	default:
		return u.resolver.Upsert(DeviceSIM{
			DeviceID:    deviceID,
			ICCID:       eventString(envelope.Data, "iccid"),
			MSISDN:      eventString(envelope.Data, "msisdn"),
			Carrier:     eventString(envelope.Data, "carrier"),
			Plan:        eventString(envelope.Data, "plan"),
			Status:      eventString(envelope.Data, "status"),
			DataUsageMB: eventFloat(envelope.Data, "data_usage_mb"),
			DataLimitMB: eventFloat(envelope.Data, "data_limit_mb"),
			ExpiryDate:  eventString(envelope.Data, "expiry_date"),
		})
	}
}

func eventString(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return strings.TrimSpace(s)
}

func eventFloat(data map[string]any, key string) float64 {
	f, _ := data[key].(float64)
	return f
}
