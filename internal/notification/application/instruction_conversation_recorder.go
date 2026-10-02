package application

import (
	"context"
	"strings"
)

// JobInstructionTextRecord is a plain-text job instruction that went out inside
// the 24h window, in the shape the conversation keeps for the template path.
type JobInstructionTextRecord struct {
	TenantID          string
	JobID             string
	DriverID          string
	DriverName        string
	Recipient         string
	Text              string
	ProviderMessageID string
	Status            string
	JobReference      string
	PickupLocation    string
	DeliveryLocation  string
	CargoDescription  string
	Priority          string
	// MessageID, when set, makes the conversation record idempotent.
	MessageID string
}

type instructionConversationRecorder interface {
	RecordJobInstructionText(ctx context.Context, record JobInstructionTextRecord) error
}

// InstructionConversationRecorder stores a text instruction as a job_instruction
// conversation message, so a driver's ARRIVED or ENROUTE reply and a failed
// receipt can be tied back to the job.
type InstructionConversationRecorder struct {
	repository  WhatsAppConversationRepository
	clock       Clock
	idGenerator IDGenerator
}

func NewInstructionConversationRecorder(repository WhatsAppConversationRepository, clock Clock, idGenerator IDGenerator) *InstructionConversationRecorder {
	return &InstructionConversationRecorder{repository: repository, clock: clock, idGenerator: idGenerator}
}

func (r *InstructionConversationRecorder) RecordJobInstructionText(ctx context.Context, record JobInstructionTextRecord) error {
	tenantID := strings.TrimSpace(record.TenantID)
	conversation, _, err := r.repository.FindConversationByTenantAndDriverPhone(ctx, tenantID, normalizeConversationPhone(record.Recipient))
	if err != nil {
		return err
	}
	_, _, err = recordOutboundConversationMessage(ctx, r.repository, r.clock, r.idGenerator, outboundConversationMessageParams{
		TenantID:          tenantID,
		MessageID:         record.MessageID,
		Recipient:         record.Recipient,
		DriverName:        record.DriverName,
		Conversation:      conversation,
		MessageType:       "text",
		Text:              record.Text,
		ProviderMessageID: record.ProviderMessageID,
		Status:            record.Status,
		Payload: map[string]any{
			"kind":              "job_instruction",
			"type":              "text",
			"job_id":            strings.TrimSpace(record.JobID),
			"driver_id":         strings.TrimSpace(record.DriverID),
			"driver_name":       strings.TrimSpace(record.DriverName),
			"job_reference":     strings.TrimSpace(record.JobReference),
			"pickup_location":   strings.TrimSpace(record.PickupLocation),
			"delivery_location": strings.TrimSpace(record.DeliveryLocation),
			"cargo_description": strings.TrimSpace(record.CargoDescription),
			"priority":          strings.TrimSpace(record.Priority),
		},
	})
	return err
}
