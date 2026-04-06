package domain

import (
	"errors"
	"strings"
	"time"
)

const (
	StatusQueued    = "queued"
	StatusSent      = "sent"
	StatusFailed    = "failed"
	StatusDelivered = "delivered"
)

type OutboxMessage struct {
	ID                string
	TenantID          string
	Channel           string
	Recipient         string
	Content           string
	Status            string
	ProviderMessageID string
	Retries           int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func NewOutboxMessage(params OutboxMessage) (OutboxMessage, error) {
	if strings.TrimSpace(params.ID) == "" {
		return OutboxMessage{}, errors.New("message id is required")
	}
	if strings.TrimSpace(params.TenantID) == "" {
		return OutboxMessage{}, errors.New("tenant id is required")
	}
	if strings.TrimSpace(params.Channel) == "" {
		return OutboxMessage{}, errors.New("channel is required")
	}
	if strings.TrimSpace(params.Recipient) == "" {
		return OutboxMessage{}, errors.New("recipient is required")
	}
	if strings.TrimSpace(params.Content) == "" {
		return OutboxMessage{}, errors.New("content is required")
	}
	if params.CreatedAt.IsZero() || params.UpdatedAt.IsZero() {
		return OutboxMessage{}, errors.New("timestamps are required")
	}

	return OutboxMessage{
		ID:                strings.TrimSpace(params.ID),
		TenantID:          strings.TrimSpace(params.TenantID),
		Channel:           strings.TrimSpace(params.Channel),
		Recipient:         strings.TrimSpace(params.Recipient),
		Content:           strings.TrimSpace(params.Content),
		Status:            strings.TrimSpace(params.Status),
		ProviderMessageID: strings.TrimSpace(params.ProviderMessageID),
		Retries:           params.Retries,
		CreatedAt:         params.CreatedAt.UTC(),
		UpdatedAt:         params.UpdatedAt.UTC(),
	}, nil
}

func (m OutboxMessage) MarkSent(status, providerMessageID string, now time.Time) OutboxMessage {
	return OutboxMessage{
		ID:                m.ID,
		TenantID:          m.TenantID,
		Channel:           m.Channel,
		Recipient:         m.Recipient,
		Content:           m.Content,
		Status:            strings.TrimSpace(status),
		ProviderMessageID: firstOutboxValue(providerMessageID, m.ProviderMessageID),
		Retries:           m.Retries,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         now.UTC(),
	}
}

func (m OutboxMessage) MarkFailed(now time.Time) OutboxMessage {
	return OutboxMessage{
		ID:                m.ID,
		TenantID:          m.TenantID,
		Channel:           m.Channel,
		Recipient:         m.Recipient,
		Content:           m.Content,
		Status:            StatusFailed,
		ProviderMessageID: m.ProviderMessageID,
		Retries:           m.Retries + 1,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         now.UTC(),
	}
}

func (m OutboxMessage) MarkRetry(status, providerMessageID string, now time.Time) OutboxMessage {
	return OutboxMessage{
		ID:                m.ID,
		TenantID:          m.TenantID,
		Channel:           m.Channel,
		Recipient:         m.Recipient,
		Content:           m.Content,
		Status:            strings.TrimSpace(status),
		ProviderMessageID: firstOutboxValue(providerMessageID, m.ProviderMessageID),
		Retries:           m.Retries + 1,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         now.UTC(),
	}
}

func (m OutboxMessage) MarkDeliveryStatus(status string, now time.Time) OutboxMessage {
	return OutboxMessage{
		ID:                m.ID,
		TenantID:          m.TenantID,
		Channel:           m.Channel,
		Recipient:         m.Recipient,
		Content:           m.Content,
		Status:            strings.TrimSpace(status),
		ProviderMessageID: m.ProviderMessageID,
		Retries:           m.Retries,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         now.UTC(),
	}
}

func firstOutboxValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
