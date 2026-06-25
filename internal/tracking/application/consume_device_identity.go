package application

import (
	"context"
	"log/slog"
	"strings"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

// ConsumeDeviceIdentityUseCase keeps the IMEI resolver cache in sync with fleet's
// device-identity events (subject tm.*.device.identity.changed).
type ConsumeDeviceIdentityUseCase struct {
	resolver IMEIResolver
	log      *slog.Logger
}

func NewConsumeDeviceIdentityUseCase(resolver IMEIResolver, log *slog.Logger) *ConsumeDeviceIdentityUseCase {
	return &ConsumeDeviceIdentityUseCase{resolver: resolver, log: log}
}

func (u *ConsumeDeviceIdentityUseCase) Handle(_ context.Context, envelope eventbus.EventEnvelope) error {
	imei, _ := envelope.Data["imei"].(string)
	imei = strings.TrimSpace(imei)
	if imei == "" {
		u.log.Warn("device_identity_missing_imei", "tenant", envelope.TenantID)
		return nil
	}

	action, _ := envelope.Data["action"].(string)
	switch action {
	case "decommissioned", "deleted":
		return u.resolver.Evict(imei)
	default:
		// vehicle_id may be null/absent (device registered but unassigned) — that
		// is fine, ingest falls back to IMEI keying until a vehicle is attached.
		deviceID, _ := envelope.Data["device_id"].(string)
		vehicleID, _ := envelope.Data["vehicle_id"].(string)
		return u.resolver.Upsert(DeviceIdentity{
			IMEI:        imei,
			TenantID:    envelope.TenantID,
			VehicleUUID: strings.TrimSpace(vehicleID),
			DeviceID:    strings.TrimSpace(deviceID),
		})
	}
}
