package application

import (
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

const (
	ErrorCodeWindowLocked         = "window_locked"
	ErrorCodeSendFailed           = "send_failed"
	ErrorCodeConversationNotFound = "conversation_not_found"
	ErrorCodeForbidden            = "forbidden"
	ErrorCodeInvalidNumber        = "invalid_number"
	ErrorCodeUndeliverable        = "undeliverable"
)

var errorTexts = map[string]string{
	ErrorCodeWindowLocked:         "The 24-hour window has closed.",
	ErrorCodeSendFailed:           "The message could not be sent.",
	ErrorCodeConversationNotFound: "Conversation not found.",
	ErrorCodeForbidden:            "You do not have permission to reply.",
	ErrorCodeInvalidNumber:        "This number is not on WhatsApp.",
	ErrorCodeUndeliverable:        "WhatsApp could not deliver this message to the recipient.",
}

type ErrorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newErrorView(code string) *ErrorView {
	return &ErrorView{Code: code, Message: errorTexts[code]}
}

// ConversationView is the wire shape of a conversation, shared by REST and the
// socket events.
type ConversationView struct {
	ID                   string    `json:"id"`
	DriverName           *string   `json:"driver_name"`
	DriverPhone          string    `json:"driver_phone"`
	LastMessagePreview   string    `json:"last_message_preview"`
	LastMessageAt        time.Time `json:"last_message_at"`
	LastMessageDirection string    `json:"last_message_direction"`
	UnreadCount          int       `json:"unread_count"`
	IsLocked             bool      `json:"is_locked"`
	WindowRemainingHours *float64  `json:"window_remaining_hours"`
	Status               string    `json:"status"`
	Priority             string    `json:"priority"`
	AssigneeUserID       *string   `json:"assignee_user_id"`
	Tags                 []string  `json:"tags"`
	FirstSeenAt          time.Time `json:"first_seen_at"`
}

type MediaView struct {
	Type     string  `json:"type"`
	URL      string  `json:"url"`
	Filename *string `json:"filename"`
	Caption  *string `json:"caption"`
}

type MessageView struct {
	ID              string     `json:"id"`
	ClientMessageID *string    `json:"client_message_id"`
	Direction       string     `json:"direction"`
	Status          string     `json:"status"`
	Text            *string    `json:"text"`
	MessageType     string     `json:"message_type"`
	CreatedAt       time.Time  `json:"created_at"`
	Error           *ErrorView `json:"error,omitempty"`
	TemplateName    *string    `json:"template_name,omitempty"`
	Media           *MediaView `json:"media,omitempty"`
	AuthorID        *string    `json:"author_id,omitempty"`
	ScheduledFor    *time.Time `json:"scheduled_for,omitempty"`
}

type MessageCreatedView struct {
	ConversationID string      `json:"conversation_id"`
	Message        MessageView `json:"message"`
}

type MessageStatusView struct {
	ConversationID  string     `json:"conversation_id"`
	MessageID       string     `json:"message_id"`
	ClientMessageID *string    `json:"client_message_id,omitempty"`
	Status          string     `json:"status"`
	Error           *ErrorView `json:"error,omitempty"`
}

type MessageDeletedView struct {
	ConversationID  string  `json:"conversation_id"`
	MessageID       string  `json:"message_id"`
	ClientMessageID *string `json:"client_message_id,omitempty"`
}

type ConversationUpdatedView struct {
	ConversationID string           `json:"conversation_id"`
	Conversation   ConversationView `json:"conversation"`
}

func NewConversationView(c domain.WhatsAppConversation, now time.Time) ConversationView {
	view := ConversationView{
		ID:                   c.ID,
		DriverPhone:          c.DriverPhone,
		LastMessagePreview:   c.LastMessagePreview,
		LastMessageAt:        domain.ConversationSortAt(c),
		LastMessageDirection: c.LastMessageDirection,
		UnreadCount:          c.UnreadCount,
		IsLocked:             c.IsLocked(now),
		Status:               c.EffectiveStatus(),
		Priority:             c.EffectivePriority(),
		Tags:                 append([]string{}, c.Tags...),
		FirstSeenAt:          c.CreatedAt,
	}
	if assignee := strings.TrimSpace(c.AssigneeUserID); assignee != "" {
		view.AssigneeUserID = &assignee
	}
	if name := strings.TrimSpace(c.DriverName); name != "" {
		view.DriverName = &name
	}
	if !view.IsLocked {
		hours := c.WindowRemainingHours(now)
		view.WindowRemainingHours = &hours
	}
	return view
}

func NewMessageView(m domain.WhatsAppConversationMessage) MessageView {
	view := MessageView{
		ID:          m.ID,
		Direction:   m.Direction,
		Status:      inboxStatus(m),
		MessageType: "other",
		CreatedAt:   m.CreatedAt,
		Error:       messageError(m),
	}
	if m.ClientMessageID != "" {
		id := m.ClientMessageID
		view.ClientMessageID = &id
	}
	switch {
	case m.MessageType == domain.WhatsAppMessageTypeNote:
		view.MessageType = domain.WhatsAppMessageTypeNote
		view.Text = textPointer(m.Text)
	case m.MessageType == "text":
		view.MessageType = "text"
		view.Text = textPointer(m.Text)
	case m.MessageType == "template":
		view.MessageType = "template"
		view.Text = textPointer(m.Text)
		view.TemplateName = textPointer(m.TemplateName)
	case isSupportedOutboundMediaType(m.MediaType):
		view.MessageType = strings.ToLower(m.MediaType)
		view.Media = mediaViewOf(m)
		view.Text = optionalText(m.Caption)
	}
	if m.Direction == domain.WhatsAppDirectionInternal || m.SenderRole == platformStaffRole {
		view.AuthorID = optionalText(m.SenderSubject)
	}
	if m.Status == domain.WhatsAppMessageStatusScheduled {
		if at, ok := domain.ScheduledAt(m); ok {
			view.ScheduledFor = &at
		}
	}
	return view
}

const platformStaffRole = "platform_staff"

func textPointer(value string) *string { return &value }

func optionalText(value string) *string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return &trimmed
	}
	return nil
}

func mediaViewOf(m domain.WhatsAppConversationMessage) *MediaView {
	view := &MediaView{Type: strings.ToLower(m.MediaType), Caption: optionalText(m.Caption)}
	if strings.TrimSpace(m.MediaID) != "" {
		view.URL = SystemMediaPathPrefix + m.ID
	}
	filename, _ := m.Payload["filename"].(string)
	view.Filename = optionalText(filename)
	return view
}

const SystemMediaPathPrefix = "/notify/system/whatsapp/media/"

func NewMessageStatusView(m domain.WhatsAppConversationMessage) MessageStatusView {
	view := MessageStatusView{
		ConversationID: m.ConversationID,
		MessageID:      m.ID,
		Status:         inboxStatus(m),
		Error:          messageError(m),
	}
	if m.ClientMessageID != "" {
		id := m.ClientMessageID
		view.ClientMessageID = &id
	}
	return view
}

var inboxStatusRank = map[string]int{"scheduled": -1, "queued": 0, "sent": 1, "delivered": 2, "read": 3}

// inboxStatus maps stored statuses (provider words included) onto the wire set.
func inboxStatus(m domain.WhatsAppConversationMessage) string {
	status := strings.ToLower(strings.TrimSpace(m.Status))
	switch status {
	case "received", "scheduled", "queued", "sent", "delivered", "read", "failed":
		return status
	case "undelivered":
		return "failed"
	}
	if m.Direction == domain.WhatsAppDirectionInbound {
		return "received"
	}
	return "sent"
}

// advancedStatus returns next only when it moves the message forward; failed
// may follow anything except a delivered or read message.
func advancedStatus(current, next string) string {
	if next == "failed" {
		if rank, ok := inboxStatusRank[current]; ok && rank >= inboxStatusRank["delivered"] {
			return current
		}
		return next
	}
	nextRank, known := inboxStatusRank[next]
	if !known {
		return current
	}
	if current == "failed" || nextRank > inboxStatusRank[current] {
		return next
	}
	return current
}

func messageError(m domain.WhatsAppConversationMessage) *ErrorView {
	raw, ok := m.Payload["error"].(map[string]any)
	if !ok {
		return nil
	}
	code := strings.TrimSpace(stringValue(raw["code"]))
	if code == "" {
		return nil
	}
	return &ErrorView{Code: code, Message: strings.TrimSpace(stringValue(raw["message"]))}
}

func errorPayload(code, message string) map[string]any {
	if strings.TrimSpace(message) == "" {
		message = errorTexts[code]
	}
	return map[string]any{"code": code, "message": message}
}
