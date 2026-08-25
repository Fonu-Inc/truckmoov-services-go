package application

import (
	"context"
	"errors"
)

// ErrInsufficientFunds means the tenant's wallet does not cover the send. It is
// a business outcome, not a fault: the caller must stop, not retry.
var ErrInsufficientFunds = errors.New("wallet balance does not cover this send")

// ErrChannelDisabled means the platform has switched the channel off. Disabled
// is both "not billable" and "not usable", so the send stops here too.
var ErrChannelDisabled = errors.New("channel is disabled")

// UsageCharge is what one send costs a tenant.
type UsageCharge struct {
	TenantID  string
	Channel   string
	Quantity  int
	Reason    string
	Reference string
}

// UsageCharger debits a tenant's wallet before a message goes out.
//
// The wallet is prepaid, so the charge has to happen first and has to be able
// to refuse. Charging after the provider has accepted the message cannot refuse
// anything — the money is already spent and the tenant overspends by whatever
// was in flight.
type UsageCharger interface {
	Charge(ctx context.Context, charge UsageCharge) error
}

// billableChannels are the channels the platform charges for. Anything absent
// here sends without a charge rather than failing: email is carried on the
// tenant's own mail server and costs the platform nothing.
var billableChannels = map[string]bool{
	"sms":      true,
	"whatsapp": true,
	"voice":    true,
	"ai":       true,
}

// IsBillable reports whether a channel is charged for.
func IsBillable(channel string) bool {
	return billableChannels[channel]
}
