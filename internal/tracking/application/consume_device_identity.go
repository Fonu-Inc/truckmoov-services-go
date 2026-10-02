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
	quiet    *Quarantine
}

func NewConsumeDeviceIdentityUseCase(resolver IMEIResolver, log *slog.Logger) *ConsumeDeviceIdentityUseCase {
	return &ConsumeDeviceIdentityUseCase{resolver: resolver, log: log}
}

func (u *ConsumeDeviceIdentityUseCase) WithQuarantine(quarantine *Quarantine) *ConsumeDeviceIdentityUseCase {
	u.quiet = quarantine
	return u
}

// ownedByAnotherTenant reports the tenant that already holds the IMEI in the
// cache when it is not the event's tenant. The first holder keeps the IMEI until
// it is decommissioned or deleted.
func (u *ConsumeDeviceIdentityUseCase) ownedByAnotherTenant(imei, tenantID string) (string, error) {
	existing, err := u.resolver.Resolve(imei)
	if err != nil || existing == nil || existing.TenantID == tenantID {
		return "", err
	}
	return existing.TenantID, nil
}

func (u *ConsumeDeviceIdentityUseCase) Handle(_ context.Context, envelope eventbus.EventEnvelope) error {
	imei, _ := envelope.Data["imei"].(string)
	imei = strings.TrimSpace(imei)
	if imei == "" {
		u.log.Warn("device_identity_missing_imei", "tenant", envelope.TenantID)
		return nil
	}

	owner, err := u.ownedByAnotherTenant(imei, envelope.TenantID)
	if err != nil {
		return err
	}
	if owner != "" {
		u.log.Warn("device_identity_imei_conflict", "imei", imei, "event_tenant", envelope.TenantID, "cached_tenant", owner)
		return nil
	}

	action, _ := envelope.Data["action"].(string)
	switch action {
	case "decommissioned", "deleted":
		return u.resolver.Evict(imei)
	default:
		u.quiet.Clear(context.Background(), imei)
		// vehicle_id may be null/absent (device registered but unassigned) — that
		// is fine, ingest falls back to IMEI keying until a vehicle is attached.
		deviceID, _ := envelope.Data["device_id"].(string)
		vehicleID, _ := envelope.Data["vehicle_id"].(string)
		suspended, _ := envelope.Data["suspended"].(bool)
		return u.resolver.Upsert(DeviceIdentity{
			Suspended:   suspended,
			IMEI:        imei,
			TenantID:    envelope.TenantID,
			VehicleUUID: strings.TrimSpace(vehicleID),
			DeviceID:    strings.TrimSpace(deviceID),
		})
	}
}
