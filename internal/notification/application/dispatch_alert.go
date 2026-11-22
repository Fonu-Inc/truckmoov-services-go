package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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

// InAppChannel lands an alert in the admin portal's notification centre. It is
// shared by every admin, so it is delivered once per alert and needs no recipient.
const InAppChannel = "in_app"

// AdminInboxAlert is one alert for the admin portal's notification centre.
type AdminInboxAlert struct {
	EventID   string
	AlertType string
	Severity  string
	Title     string
	Body      string
}

// AdminInbox records in-app alerts. Recording the same EventID twice is a no-op.
type AdminInbox interface {
	Record(ctx context.Context, alert AdminInboxAlert) error
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
	"new_tenant_signup":   true,
	"system_errors":       true,
	"billing_alerts":      true,
	"security_alerts":     true,
	"installation_issues": true,
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
	inbox    AdminInbox
	claims   EventClaimStore
	log      *slog.Logger
}

func NewDispatchAlertUseCase(
	settings AlertSettingsReader,
	send *SendMessageUseCase,
	inbox AdminInbox,
	claims EventClaimStore,
	log *slog.Logger,
) *DispatchAlertUseCase {
	return &DispatchAlertUseCase{settings: settings, send: send, inbox: inbox, claims: claims, log: log}
}

func (u *DispatchAlertUseCase) Handle(ctx context.Context, envelope eventbus.EventEnvelope) error {
	alertType, _ := envelope.Data["alert_type"].(string)
	severity, _ := envelope.Data["severity"].(string)
	subject, _ := envelope.Data["subject"].(string)
	body, _ := envelope.Data["body"].(string)

	if envelope.ID == "" {
		u.log.Warn("alert_without_id", "alert_type", alertType)
		return nil
	}

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
	if hasChannel(setting.Channels, InAppChannel) {
		key := strings.Join([]string{"alert", envelope.ID, InAppChannel}, "|")
		_, inboxErr := sendOnce(ctx, u.claims, key, func() error {
			if u.inbox == nil {
				return fmt.Errorf("admin inbox is not configured")
			}
			return u.inbox.Record(ctx, AdminInboxAlert{
				EventID:   envelope.ID,
				AlertType: alertType,
				Severity:  severity,
				Title:     subject,
				Body:      body,
			})
		})
		if inboxErr != nil {
			failures++
			u.log.Warn("alert_send_failed", "alert_type", alertType, "channel", InAppChannel, "error", inboxErr)
		}
	}

	for _, recipient := range setting.Recipients {
		for _, channel := range setting.Channels {
			if channel == InAppChannel {
				continue
			}
			key := strings.Join([]string{"alert", envelope.ID, recipient, channel}, "|")
			_, sendErr := sendOnce(ctx, u.claims, key, func() error {
				// Literal, not taken from the envelope: a platform alert is the
				// platform's own cost whatever the payload claims.
				_, err := u.send.Execute(ctx, SendMessageCommand{
					TenantID:         SystemTenantID,
					Channel:          channel,
					Recipient:        recipient,
					Content:          content,
					NotificationType: domain.NotificationTypeGeneric,
				})
				return err
			})
			if sendErr != nil {
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
	if len(setting.Channels) == 0 {
		return "no_channels"
	}
	// An alert with no recipient goes nowhere unless it is shown in-app.
	// Reported as unroutable rather than counted as sent.
	if len(setting.Recipients) == 0 && !hasChannel(setting.Channels, InAppChannel) {
		return "no_recipients"
	}
	if severityRank[severity] < severityRank[setting.MinSeverity] {
		return "below_severity"
	}
	return ""
}
