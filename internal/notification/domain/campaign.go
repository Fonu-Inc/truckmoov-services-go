package domain

import (
	"errors"
	"strings"
	"time"
)

// Campaign statuses. Sending is deliberately not the same as completed: a
// broadcast reaches its recipients over seconds or minutes, and some never
// arrive, so the status has to describe a partial state rather than a binary
// one.
const (
	CampaignStatusDraft     = "draft"
	CampaignStatusScheduled = "scheduled"
	CampaignStatusSending   = "sending"
	CampaignStatusCompleted = "completed"
	CampaignStatusFailed    = "failed"
	CampaignStatusCancelled = "cancelled"
)

// Per-recipient delivery states. Read is only reachable on channels that report
// it; SMS never will, so a read count means nothing without knowing which
// channels a campaign used.
const (
	RecipientStateQueued     = "queued"
	RecipientStateSent       = "sent"
	RecipientStateDelivered  = "delivered"
	RecipientStateRead       = "read"
	RecipientStateFailed     = "failed"
	RecipientStateSuppressed = "suppressed"
)

var campaignChannels = map[string]bool{"whatsapp": true, "sms": true, "email": true}

func IsCampaignChannel(channel string) bool {
	return campaignChannels[strings.ToLower(strings.TrimSpace(channel))]
}

// ChannelReportsRead says whether a channel ever confirms a read receipt.
// Counting reads against messages sent over SMS would report every campaign as
// unread.
func ChannelReportsRead(channel string) bool {
	return strings.ToLower(strings.TrimSpace(channel)) == "whatsapp"
}

type Campaign struct {
	ID       string
	TenantID string
	Name     string
	Channels []string
	// Body per channel. An SMS body and a WhatsApp body are not
	// interchangeable, and one field for both produces a message that reads
	// wrongly on at least one of them.
	Bodies map[string]string
	// Required when WhatsApp is a channel: business-initiated WhatsApp messages
	// outside a service window are template-gated by the provider.
	TemplateName string
	Status       string
	ScheduledFor *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NewCampaign(params Campaign) (Campaign, error) {
	if strings.TrimSpace(params.ID) == "" {
		return Campaign{}, errors.New("campaign id is required")
	}
	if strings.TrimSpace(params.TenantID) == "" {
		return Campaign{}, errors.New("tenant id is required")
	}
	if strings.TrimSpace(params.Name) == "" {
		return Campaign{}, errors.New("campaign name is required")
	}
	if len(params.Channels) == 0 {
		return Campaign{}, errors.New("at least one channel is required")
	}

	normalized := make([]string, 0, len(params.Channels))
	seen := map[string]bool{}
	for _, channel := range params.Channels {
		lower := strings.ToLower(strings.TrimSpace(channel))
		if !IsCampaignChannel(lower) {
			return Campaign{}, errors.New("channel must be one of: whatsapp, sms, email")
		}
		if seen[lower] {
			continue
		}
		seen[lower] = true
		normalized = append(normalized, lower)
	}

	bodies := map[string]string{}
	for _, channel := range normalized {
		body := strings.TrimSpace(params.Bodies[channel])
		if body == "" {
			return Campaign{}, errors.New("a message body is required for every selected channel")
		}
		bodies[channel] = body
	}

	if seen["whatsapp"] && strings.TrimSpace(params.TemplateName) == "" {
		return Campaign{}, errors.New("a WhatsApp campaign needs an approved template")
	}

	status := params.Status
	if status == "" {
		status = CampaignStatusDraft
	}

	return Campaign{
		ID:           strings.TrimSpace(params.ID),
		TenantID:     strings.TrimSpace(params.TenantID),
		Name:         strings.TrimSpace(params.Name),
		Channels:     normalized,
		Bodies:       bodies,
		TemplateName: strings.TrimSpace(params.TemplateName),
		Status:       status,
		ScheduledFor: params.ScheduledFor,
		CreatedAt:    params.CreatedAt,
		UpdatedAt:    params.UpdatedAt,
	}, nil
}

// CampaignRecipient is one message to one driver on one channel. A driver
// targeted on two channels produces two rows, because each is delivered,
// charged and reported separately.
type CampaignRecipient struct {
	ID          string
	CampaignID  string
	TenantID    string
	DriverID    string
	Channel     string
	Destination string
	State       string
	// Set once the provider accepts the message, so a later receipt can be
	// matched back to this row.
	ProviderMessageID string
	FailureReason     string
	SentAt            *time.Time
	DeliveredAt       *time.Time
	ReadAt            *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CanRetry reports whether this recipient may be sent to again.
//
// Only a failure may be retried. Retrying a recipient who already received the
// message sends it twice and charges twice.
func (r CampaignRecipient) CanRetry() bool {
	return r.State == RecipientStateFailed
}

// IsTerminal reports whether this recipient can still change on its own.
// Delivered is not terminal on a channel that reports reads.
func (r CampaignRecipient) IsTerminal() bool {
	switch r.State {
	case RecipientStateFailed, RecipientStateSuppressed, RecipientStateRead:
		return true
	case RecipientStateDelivered:
		return !ChannelReportsRead(r.Channel)
	default:
		return false
	}
}

// CampaignProgress is what the list and the summary figures are built from.
type CampaignProgress struct {
	Recipients int
	Sent       int
	Delivered  int
	Read       int
	Failed     int
}

func SummarizeRecipients(recipients []CampaignRecipient) CampaignProgress {
	progress := CampaignProgress{Recipients: len(recipients)}
	for _, recipient := range recipients {
		switch recipient.State {
		case RecipientStateFailed, RecipientStateSuppressed:
			progress.Failed++
		case RecipientStateSent:
			progress.Sent++
		case RecipientStateDelivered:
			progress.Sent++
			progress.Delivered++
		case RecipientStateRead:
			// A read message was delivered, and a delivered one was sent. The
			// states are a progression, so the counts have to nest or the
			// delivery rate reads lower than it was.
			progress.Sent++
			progress.Delivered++
			progress.Read++
		}
	}
	return progress
}

// ResolveCampaignStatus derives the campaign's state from its recipients.
//
// Stored rather than computed would need every receipt to remember to update
// it, and receipts arrive late and out of order.
func ResolveCampaignStatus(recipients []CampaignRecipient) string {
	if len(recipients) == 0 {
		return CampaignStatusFailed
	}

	terminal, failed := 0, 0
	for _, recipient := range recipients {
		if recipient.IsTerminal() {
			terminal++
		}
		if recipient.State == RecipientStateFailed || recipient.State == RecipientStateSuppressed {
			failed++
		}
	}

	if terminal < len(recipients) {
		return CampaignStatusSending
	}
	if failed == len(recipients) {
		return CampaignStatusFailed
	}
	return CampaignStatusCompleted
}
