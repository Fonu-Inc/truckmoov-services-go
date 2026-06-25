package application

// DeviceIdentity is the canonical mapping fleet owns for a tracker. The IMEI is
// the single natural key across fleet ↔ Traccar ↔ tracking; VehicleUUID/DeviceID
// are the fleet-side UUIDs telemetry must be rekeyed onto.
type DeviceIdentity struct {
	IMEI        string `json:"imei"`
	TenantID    string `json:"tenant_id"`
	VehicleUUID string `json:"vehicle_uuid"`
	DeviceID    string `json:"device_id"`
}

// IMEIResolver caches fleet device-identity events so ingest can resolve an
// incoming IMEI to its tenant/vehicle/device without a synchronous fleet call.
type IMEIResolver interface {
	Resolve(imei string) (*DeviceIdentity, error)
	Upsert(identity DeviceIdentity) error
	Evict(imei string) error
}
