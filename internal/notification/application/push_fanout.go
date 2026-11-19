package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
	"github.com/truckmoov/services-go/pkg/identity"
)

// PushRecipient is a tenant user as auth describes them for notifications.
type PushRecipient struct {
	ID               string
	Role             string
	OrganizationType string
	// Missing keys mean auth's default, which is on for every key used here.
	Preferences map[string]bool
}

type PushRecipientReader interface {
	Recipients(ctx context.Context, tenantID string) ([]PushRecipient, error)
}

// VehicleScopeReader answers which vehicles a user may see. wholeTenant is
// true when the user is not narrowed to a list.
type VehicleScopeReader interface {
	VehicleScope(ctx context.Context, tenantID, userID string) (vehicleIDs []string, wholeTenant bool, err error)
}

// PushCounter counts within a window that starts at the first increment.
type PushCounter interface {
	Increment(ctx context.Context, key string, window time.Duration) (int64, error)
	Decrement(ctx context.Context, key string) error
}

type PushLimits struct {
	CoalesceWindow time.Duration
	UserCap        int
	UserCapWindow  time.Duration
}

var opsRoles = map[string]bool{
	"ENTERPRISE_ADMIN":   true,
	"FLEET_ADMIN":        true,
	"AGGREGATOR_ADMIN":   true,
	"OPERATIONS_MANAGER": true,
	"DISPATCHER":         true,
}

const ownerOperatorRole = "OWNER_OPERATOR"

type pushAudience int

const (
	audienceOps pushAudience = iota
	audienceMessaging
)

// pushEvent is one bus event already reduced to what any recipient would get.
type pushEvent struct {
	tenantID    string
	eventID     string
	actorID     string
	audience    pushAudience
	vehicleID   string
	preference  string
	coalesceKey string
	message     domain.PushMessage
}

// PushFanout turns bus events into pushes to the people who should hear about
// them, and holds back repeats and floods.
type PushFanout struct {
	send       *SendMessageUseCase
	recipients PushRecipientReader
	scope      VehicleScopeReader
	devices    PushDeviceRepository
	claims     EventClaimStore
	counter    PushCounter
	limits     PushLimits
	log        *slog.Logger
}

func NewPushFanout(
	send *SendMessageUseCase,
	recipients PushRecipientReader,
	scope VehicleScopeReader,
	devices PushDeviceRepository,
	claims EventClaimStore,
	counter PushCounter,
	limits PushLimits,
	log *slog.Logger,
) *PushFanout {
	return &PushFanout{send: send, recipients: recipients, scope: scope, devices: devices, claims: claims, counter: counter, limits: limits, log: log}
}

func (f *PushFanout) HandleAlert(ctx context.Context, envelope eventbus.EventEnvelope) error {
	alertType := dataString(envelope, "alert_type")
	severity := dataString(envelope, "severity")
	vehicleID := dataString(envelope, "vehicle_id")
	if severityRank[severity] < severityRank["medium"] {
		return nil
	}
	id := dataString(envelope, "alert_id")
	if id == "" {
		id = vehicleID
	}
	preference := "incidentAlerts"
	if strings.HasPrefix(alertType, "geofence_") {
		preference = "geofenceAlerts"
	}
	title := humanize(alertType)
	if zone := dataString(envelope, "zoneName"); zone != "" {
		title += " · " + zone
	}
	return f.deliver(ctx, pushEvent{
		tenantID:    envelope.TenantID,
		eventID:     envelope.ID,
		actorID:     actorID(envelope),
		audience:    audienceOps,
		vehicleID:   vehicleID,
		preference:  preference,
		coalesceKey: "alert:" + vehicleID + ":" + alertType,
		message: domain.PushMessage{
			Title:    title,
			Body:     strings.ToUpper(severity[:1]) + severity[1:] + " severity vehicle alert",
			Kind:     domain.PushKindAlert,
			ID:       id,
			Category: "alerts",
		},
	})
}

var jobStatusTitles = map[string]string{
	"assigned":  "Job assigned",
	"cancelled": "Job cancelled",
	"delivered": "Job delivered",
}

func (f *PushFanout) HandleJobStatus(ctx context.Context, envelope eventbus.EventEnvelope) error {
	status := dataString(envelope, "new_status")
	title, ok := jobStatusTitles[status]
	if !ok {
		return nil
	}
	jobID := dataString(envelope, "job_id")
	preference := "jobCompletion"
	if status == "assigned" {
		preference = "jobAssignment"
	}
	return f.deliver(ctx, pushEvent{
		tenantID:    envelope.TenantID,
		eventID:     envelope.ID,
		actorID:     actorID(envelope),
		audience:    audienceOps,
		vehicleID:   dataString(envelope, "assigned_vehicle_id"),
		preference:  preference,
		coalesceKey: "job:" + jobID,
		message: domain.PushMessage{
			Title:    title,
			Body:     "A job is now " + status + ".",
			Kind:     domain.PushKindJob,
			ID:       jobID,
			Category: "jobs",
		},
	})
}

// HandleJobOffer covers both directions: a created offer arrives on the target
// tenant's subject and a response on the offering tenant's, so the envelope's
// tenant is always the one to tell.
func (f *PushFanout) HandleJobOffer(ctx context.Context, envelope eventbus.EventEnvelope) error {
	jobID := dataString(envelope, "job_id")
	title, body := "New job offer", "A job has been offered to you."
	if strings.HasSuffix(envelope.Type, ".responded") || strings.HasSuffix(envelope.Subject, ".responded") {
		status := dataString(envelope, "status")
		title, body = "Job offer "+status, "Your job offer was "+status+"."
	}
	return f.deliver(ctx, pushEvent{
		tenantID:    envelope.TenantID,
		eventID:     envelope.ID,
		actorID:     actorID(envelope),
		audience:    audienceOps,
		preference:  "jobAssignment",
		coalesceKey: "job:" + jobID,
		message: domain.PushMessage{
			Title:    title,
			Body:     body,
			Kind:     domain.PushKindJobOffer,
			ID:       jobID,
			Category: "job_offers",
		},
	})
}

func (f *PushFanout) HandleActivity(ctx context.Context, envelope eventbus.EventEnvelope) error {
	if dataString(envelope, "action") != "whatsapp.message.received" {
		return nil
	}
	metadata, _ := envelope.Data["metadata"].(map[string]any)
	conversationID, _ := metadata["conversation_id"].(string)
	if conversationID == "" {
		return nil
	}
	body, _ := metadata["preview"].(string)
	if body == "" {
		body = dataString(envelope, "summary")
	}
	return f.deliver(ctx, pushEvent{
		tenantID:    envelope.TenantID,
		eventID:     envelope.ID,
		actorID:     actorID(envelope),
		audience:    audienceMessaging,
		preference:  "pushAlerts",
		coalesceKey: "conversation:" + conversationID,
		message: domain.PushMessage{
			Title:    "New WhatsApp message",
			Body:     truncate(body, 140),
			Kind:     domain.PushKindConversation,
			ID:       conversationID,
			Category: "messages",
		},
	})
}

func (f *PushFanout) deliver(ctx context.Context, event pushEvent) error {
	if event.eventID == "" || event.tenantID == "" {
		return nil
	}
	recipients, err := f.recipients.Recipients(ctx, event.tenantID)
	if err != nil {
		return fmt.Errorf("read push recipients: %w", err)
	}
	withDevices, err := f.devices.UsersWithDevices(ctx, event.tenantID)
	if err != nil {
		return fmt.Errorf("read push devices: %w", err)
	}
	var failures int
	for _, recipient := range recipients {
		if !withDevices[recipient.ID] || recipient.ID == event.actorID || !f.wants(ctx, event, recipient) {
			continue
		}
		key := strings.Join([]string{"push", event.eventID, recipient.ID, domain.ChannelPush}, "|")
		if _, sendErr := sendOnce(ctx, f.claims, key, func() error {
			return f.pushWithLimits(ctx, event, recipient.ID)
		}); sendErr != nil {
			failures++
			f.log.Warn("push_send_failed", "event_id", event.eventID, "kind", event.message.Kind, "error", sendErr)
		}
	}
	if failures > 0 {
		return fmt.Errorf("push %s: %d recipient(s) failed", event.eventID, failures)
	}
	return nil
}

func (f *PushFanout) wants(ctx context.Context, event pushEvent, recipient PushRecipient) bool {
	if !preferenceOn(recipient, "pushAlerts") || !preferenceOn(recipient, event.preference) {
		return false
	}
	if event.audience == audienceMessaging {
		return holdsMessaging(recipient.Role, event.tenantID)
	}
	if opsRoles[recipient.Role] {
		return true
	}
	if recipient.Role != ownerOperatorRole || event.vehicleID == "" {
		return false
	}
	vehicleIDs, wholeTenant, err := f.scope.VehicleScope(ctx, event.tenantID, recipient.ID)
	if err != nil {
		// Fail closed, as the realtime gateway does: an unresolved scope must
		// not leak another operator's vehicle.
		f.log.Warn("push_scope_unresolved", "user_id", recipient.ID, "error", err)
		return false
	}
	if wholeTenant {
		return true
	}
	for _, id := range vehicleIDs {
		if id == event.vehicleID {
			return true
		}
	}
	return false
}

// pushWithLimits applies the coalescing window and the per-user cap. The
// coalescing claim is given back when the send fails, so a redelivery is not
// mistaken for a repeat.
func (f *PushFanout) pushWithLimits(ctx context.Context, event pushEvent, userID string) error {
	coalesce := "push-coalesce|" + userID + "|" + event.coalesceKey
	first, err := f.claims.Claim(ctx, coalesce, f.limits.CoalesceWindow)
	if err != nil || !first {
		return err
	}
	capKey := "push-cap|" + userID
	count, err := f.counter.Increment(ctx, capKey, f.limits.UserCapWindow)
	if err != nil {
		_ = f.claims.Release(ctx, coalesce)
		return err
	}
	message := event.message
	switch {
	case count <= int64(f.limits.UserCap):
	case count == int64(f.limits.UserCap)+1:
		// The count of what is still to come is unknowable here, so the digest
		// does not promise a number.
		message = domain.PushMessage{
			Title:    "Fleet Pro",
			Body:     "More updates — open Fleet Pro",
			Kind:     domain.PushKindAlert,
			Category: "digest",
		}
		message.CollapseKey = "digest:" + userID
	default:
		return nil
	}
	if message.CollapseKey == "" {
		message.CollapseKey = event.coalesceKey
	}
	messageID := "push_" + strings.ReplaceAll(event.eventID+"_"+userID, "|", "_")
	message.DeliveryID = messageID
	content, err := json.Marshal(message)
	if err == nil {
		_, err = f.send.Execute(ctx, SendMessageCommand{
			TenantID:         event.tenantID,
			Channel:          domain.ChannelPush,
			Recipient:        userID,
			Content:          string(content),
			NotificationType: "push_" + message.Kind,
			MessageID:        messageID,
		})
	}
	if err != nil {
		// A failed attempt must not use up the user's cap, or retries of one
		// event silence every other event in the window.
		_ = f.claims.Release(ctx, coalesce)
		_ = f.counter.Decrement(ctx, capKey)
	}
	return err
}

func preferenceOn(recipient PushRecipient, key string) bool {
	on, set := recipient.Preferences[key]
	return !set || on
}

func holdsMessaging(role, tenantID string) bool {
	for _, permission := range identity.RolePermissions(role, tenantID) {
		if strings.HasPrefix(permission, "tenant.messaging.") {
			return true
		}
	}
	return false
}

func dataString(envelope eventbus.EventEnvelope, key string) string {
	value, _ := envelope.Data[key].(string)
	return strings.TrimSpace(value)
}

func actorID(envelope eventbus.EventEnvelope) string {
	id, _ := envelope.Actor["id"].(string)
	return id
}

func humanize(value string) string {
	if value == "" {
		return "Vehicle alert"
	}
	words := strings.ReplaceAll(value, "_", " ")
	return strings.ToUpper(words[:1]) + words[1:]
}

func truncate(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max-1]) + "…"
}
