package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/identity"
)

// AlertSetting is who the platform tells about one kind of event, and how.
//
// Owned by the auth service. Copied nowhere: this service reads it at the
// moment it needs it, so there is one answer to "who gets told about billing
// failures" rather than one per service.
type AlertSetting struct {
	AlertType   string
	Enabled     bool
	Channels    []string
	Recipients  []string
	MinSeverity string
}

// AlertSettingsReader fetches the configuration for one alert type.
type AlertSettingsReader interface {
	// Returns nil when nothing is configured, which is handled the same way as
	// an alert switched off.
	ForAlertType(ctx context.Context, alertType string) (*AlertSetting, error)
}

// severityRank orders severities so a minimum can be compared. Unknown
// severities rank lowest, so a typo suppresses rather than pages everyone.
var severityRank = map[string]int{"low": 0, "medium": 1, "high": 2, "critical": 3}

// SystemTenantID owns platform alerts. They belong to the platform, not to any
// customer, so they are never attributed to or charged against a tenant.
const SystemTenantID = identity.SystemTenantID

// knownAlertTypes is the closed set the platform raises.
//
// The type arrives inside an event payload, so it is only as trustworthy as
// whoever can publish to the bus. It ends up in a URL path and as a cache key,
// which makes an unchecked value both a way to reach a different endpoint and a
// way to grow the cache without bound.
var knownAlertTypes = map[string]bool{
	"new_tenant_signup": true,
	"system_errors":     true,
	"billing_alerts":    true,
	"security_alerts":   true,
}

// IsKnownAlertType reports whether the platform recognises this alert type.
func IsKnownAlertType(alertType string) bool {
	return knownAlertTypes[alertType]
}

// DispatchAlertUseCase turns a raised alert into messages to platform staff.
//
// The raiser knows what happened and nothing about who should hear it. This
// resolves that, so adding a recipient is a settings change rather than a
// deployment.
type DispatchAlertUseCase struct {
	settings AlertSettingsReader
	send     *SendMessageUseCase
	log      *slog.Logger
}

func NewDispatchAlertUseCase(
	settings AlertSettingsReader,
	send *SendMessageUseCase,
	log *slog.Logger,
) *DispatchAlertUseCase {
	return &DispatchAlertUseCase{settings: settings, send: send, log: log}
}

func (u *DispatchAlertUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	alertType, _ := envelope.Data["alert_type"].(string)
	severity, _ := envelope.Data["severity"].(string)
	subject, _ := envelope.Data["subject"].(string)
	body, _ := envelope.Data["body"].(string)

	if subject == "" || !IsKnownAlertType(alertType) {
		// Not retried: an unrecognised type can never become valid, and the
		// value is not echoed back into a log field that might be read as one.
		u.log.Warn("alert_invalid", "known_type", IsKnownAlertType(alertType))
		return nil
	}

	// The subject this consumer listens on is platform-scoped, but the tenant
	// in the payload is just a field. Trusting it would let a publisher have a
	// platform alert attributed to a customer, and charged to them on any
	// billable channel.
	if envelope.TenantID != SystemTenantID {
		u.log.Warn("alert_wrong_tenant", "alert_type", alertType)
		return nil
	}

	setting, err := u.settings.ForAlertType(ctx, alertType)
	if err != nil {
		// Retried. Dropping the alert because the configuration could not be
		// read would lose exactly the events worth hearing about.
		return fmt.Errorf("read alert settings for %s: %w", alertType, err)
	}

	if reason := suppressionReason(setting, severity); reason != "" {
		// Said out loud. An alert that goes nowhere is otherwise indistinguishable
		// from one that was never raised.
		u.log.Info("alert_suppressed", "alert_type", alertType, "reason", reason)
		return nil
	}

	content := subject
	if body != "" {
		content = subject + "\n\n" + body
	}

	var failures int
	for _, recipient := range setting.Recipients {
		for _, channel := range setting.Channels {
			if _, sendErr := u.send.Execute(ctx, SendMessageCommand{
				// Literal, not taken from the envelope: a platform alert is the
				// platform's own cost whatever the payload claims.
				TenantID:         SystemTenantID,
				Channel:          channel,
				Recipient:        recipient,
				Content:          content,
				NotificationType: domain.NotificationTypeGeneric,
			}); sendErr != nil {
				// One unreachable recipient must not stop the others being told.
				failures++
				u.log.Warn("alert_send_failed",
					"alert_type", alertType, "channel", channel, "error", sendErr)
			}
		}
	}

	if failures > 0 {
		return fmt.Errorf("alert %s: %d delivery attempt(s) failed", alertType, failures)
	}
	return nil
}

// suppressionReason explains why an alert will not be sent, or returns "".
func suppressionReason(setting *AlertSetting, severity string) string {
	if setting == nil {
		return "not_configured"
	}
	if !setting.Enabled {
		return "disabled"
	}
	// An alert with no recipient goes nowhere. Reported as unroutable rather
	// than counted as sent.
	if len(setting.Recipients) == 0 {
		return "no_recipients"
	}
	if len(setting.Channels) == 0 {
		return "no_channels"
	}
	if severityRank[severity] < severityRank[setting.MinSeverity] {
		return "below_severity"
	}
	return ""
}
