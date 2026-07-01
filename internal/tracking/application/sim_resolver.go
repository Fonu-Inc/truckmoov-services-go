package application

type DeviceSIM struct {
	DeviceID    string  `json:"device_id"`
	ICCID       string  `json:"iccid"`
	MSISDN      string  `json:"msisdn"`
	Carrier     string  `json:"carrier"`
	Plan        string  `json:"plan"`
	Status      string  `json:"status"`
	DataUsageMB float64 `json:"data_usage_mb"`
	DataLimitMB float64 `json:"data_limit_mb"`
	ExpiryDate  string  `json:"expiry_date,omitempty"`
}

type SIMResolver interface {
	Resolve(deviceID string) (*DeviceSIM, error)
	Upsert(sim DeviceSIM) error
	Evict(deviceID string) error
}
