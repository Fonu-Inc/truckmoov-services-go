package application

import (
	"context"
	"fmt"

	"github.com/truckmoov/services-go/internal/tracking/domain"
)

type PublishAlertCommand struct {
	TenantID  string
	AlertType string
	Severity  string
	VehicleID string
	JobID     string
	Details   any
}

type PublishAlertResult struct {
	Published bool   `json:"published"`
	Subject   string `json:"subject"`
	EventID   string `json:"event_id"`
	AlertID   string `json:"alert_id"`
}

type PublishAlertUseCase struct {
	publisher     TrackingEventPublisher
	clock         Clock
	idGenerator   IDGenerator
	defaultTenant string
}

func NewPublishAlertUseCase(
	publisher TrackingEventPublisher,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
) *PublishAlertUseCase {
	return &PublishAlertUseCase{
		publisher:     publisher,
		clock:         clock,
		idGenerator:   idGenerator,
		defaultTenant: defaultTenant,
	}
}

func (u *PublishAlertUseCase) Execute(
	ctx context.Context,
	command PublishAlertCommand,
) (PublishAlertResult, error) {
	tenantID := command.TenantID
	if tenantID == "" {
		tenantID = u.defaultTenant
	}

	severity := command.Severity
	if severity == "" {
		severity = "medium"
	}

	alertID := u.idGenerator.New("alt")
	event, err := domain.NewAlertTriggered(domain.AlertTriggered{
		EventID:    u.idGenerator.New("evt"),
		OccurredAt: u.clock.Now(),
		TenantID:   tenantID,
		AlertID:    alertID,
		AlertType:  command.AlertType,
		Severity:   severity,
		VehicleID:  command.VehicleID,
		JobID:      command.JobID,
		Details:    command.Details,
	})
	if err != nil {
		return PublishAlertResult{}, fmt.Errorf("build alert event: %w", err)
	}

	if err := u.publisher.PublishAlertTriggered(ctx, event); err != nil {
		return PublishAlertResult{}, fmt.Errorf("publish alert event: %w", err)
	}

	return PublishAlertResult{
		Published: true,
		Subject:   event.Subject(),
		EventID:   event.EventID,
		AlertID:   event.AlertID,
	}, nil
}
