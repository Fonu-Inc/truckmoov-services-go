package domain

import (
	"errors"
	"strings"
	"time"
)

const ChannelPush = "push"

// Push kinds the mobile app knows how to open.
const (
	PushKindAlert        = "alert"
	PushKindJob          = "job"
	PushKindJobOffer     = "job_offer"
	PushKindTicket       = "ticket"
	PushKindConversation = "conversation"
	PushKindVehicle      = "vehicle"
	PushKindDevice       = "device"
)

// PushDevice is one app install that can receive pushes for a user.
type PushDevice struct {
	Token      string
	TenantID   string
	UserID     string
	Platform   string
	AppVersion string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

var ErrInvalidPushDevice = errors.New("token and a platform of ios or android are required")

func (d PushDevice) Validate() error {
	if strings.TrimSpace(d.Token) == "" || len(d.Token) > 4096 || len(d.AppVersion) > 64 {
		return ErrInvalidPushDevice
	}
	if d.Platform != "ios" && d.Platform != "android" {
		return ErrInvalidPushDevice
	}
	if strings.TrimSpace(d.TenantID) == "" || strings.TrimSpace(d.UserID) == "" {
		return errors.New("tenant and user are required")
	}
	return nil
}

// PushMessage travels as an outbox message's content, so it goes through the
// same send, log and retry path as every other channel.
type PushMessage struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	CollapseKey string `json:"collapseKey"`
	Category    string `json:"category"`
	// DeliveryID is stable across redeliveries of one push, so each device
	// gets it at most once.
	DeliveryID string `json:"deliveryId,omitempty"`
}
