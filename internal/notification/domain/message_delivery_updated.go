package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type MessageDeliveryUpdated struct {
	EventID    string
	OccurredAt time.Time
	TenantID   string
	MessageID  string
	Channel    string
	Status     string
	ErrorText  string
}

func NewMessageDeliveryUpdated(params MessageDeliveryUpdated) (MessageDeliveryUpdated, error) {
	if strings.TrimSpace(params.EventID) == "" {
		return MessageDeliveryUpdated{}, errors.New("event id is required")
	}
	if strings.TrimSpace(params.TenantID) == "" {
		return MessageDeliveryUpdated{}, errors.New("tenant id is required")
	}
	if strings.TrimSpace(params.MessageID) == "" {
		return MessageDeliveryUpdated{}, errors.New("message id is required")
	}
	if strings.TrimSpace(params.Channel) == "" {
		return MessageDeliveryUpdated{}, errors.New("channel is required")
	}
	if strings.TrimSpace(params.Status) == "" {
		return MessageDeliveryUpdated{}, errors.New("status is required")
	}
	if params.OccurredAt.IsZero() {
		return MessageDeliveryUpdated{}, errors.New("occurred_at is required")
	}

	return MessageDeliveryUpdated{
		EventID:    strings.TrimSpace(params.EventID),
		OccurredAt: params.OccurredAt.UTC(),
		TenantID:   strings.TrimSpace(params.TenantID),
		MessageID:  strings.TrimSpace(params.MessageID),
		Channel:    strings.TrimSpace(params.Channel),
		Status:     strings.TrimSpace(params.Status),
		ErrorText:  strings.TrimSpace(params.ErrorText),
	}, nil
}

func (e MessageDeliveryUpdated) Subject() string {
	return fmt.Sprintf("tm.%s.message.delivery.updated", e.TenantID)
}
