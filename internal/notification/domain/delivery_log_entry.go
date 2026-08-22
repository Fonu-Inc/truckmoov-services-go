package domain

import (
	"errors"
	"strings"
	"time"
)

// NotificationType groups entries so one log serves the whole platform rather
// than each feature keeping its own. KYC is one type within it, not a log of
// its own.
const (
	NotificationTypeGeneric            = "generic"
	NotificationTypeKycDecision        = "kyc_decision"
	NotificationTypeComplianceReminder = "compliance_reminder"
	NotificationTypeJobAssignment      = "job_assignment"
	NotificationTypeCampaign           = "campaign"
	NotificationTypeSpendingLimit      = "spending_limit"
)

// DeliveryLogEntry answers "did we tell them", so it stores the message as it
// was sent. Regenerating it from a template later would show what the template
// says now, which is not necessarily what the recipient read.
type DeliveryLogEntry struct {
	ID                string
	TenantID          string
	NotificationType  string
	Channel           string
	Recipient         string
	Subject           string
	Content           string
	Status            string
	ProviderMessageID string
	FailureReason     string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeliveredAt       *time.Time
}

func NewDeliveryLogEntry(params DeliveryLogEntry) (DeliveryLogEntry, error) {
	if strings.TrimSpace(params.ID) == "" {
		return DeliveryLogEntry{}, errors.New("id is required")
	}
	if strings.TrimSpace(params.TenantID) == "" {
		return DeliveryLogEntry{}, errors.New("tenant id is required")
	}
	if strings.TrimSpace(params.Channel) == "" {
		return DeliveryLogEntry{}, errors.New("channel is required")
	}
	if strings.TrimSpace(params.Recipient) == "" {
		return DeliveryLogEntry{}, errors.New("recipient is required")
	}

	entry := params
	if strings.TrimSpace(entry.NotificationType) == "" {
		entry.NotificationType = NotificationTypeGeneric
	}
	if strings.TrimSpace(entry.Status) == "" {
		entry.Status = StatusQueued
	}
	return entry, nil
}

// DeliveryLogFilters narrows a tenant's log. The log is high volume, so a query
// without a limit is not offered.
type DeliveryLogFilters struct {
	NotificationType string
	Channel          string
	Status           string
	Limit            int
	Offset           int
}
