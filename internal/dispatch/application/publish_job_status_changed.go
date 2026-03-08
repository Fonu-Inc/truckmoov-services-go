package application

import (
	"context"
	"fmt"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type JobStatusChangedPublisher interface {
	PublishJobStatusChanged(ctx context.Context, event domain.JobStatusChanged) error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	New() string
}

type PublishJobStatusChangedCommand struct {
	TenantID          string
	JobID             string
	PreviousStatus    string
	NewStatus         string
	AssignedDriverID  string
	AssignedVehicleID string
	Reason            string
}

type PublishJobStatusChangedResult struct {
	Published bool   `json:"published"`
	Subject   string `json:"subject"`
	EventID   string `json:"event_id"`
}

type PublishJobStatusChangedUseCase struct {
	publisher     JobStatusChangedPublisher
	clock         Clock
	idGenerator   IDGenerator
	defaultTenant string
}

func NewPublishJobStatusChangedUseCase(
	publisher JobStatusChangedPublisher,
	clock Clock,
	idGenerator IDGenerator,
	defaultTenant string,
) *PublishJobStatusChangedUseCase {
	return &PublishJobStatusChangedUseCase{
		publisher:     publisher,
		clock:         clock,
		idGenerator:   idGenerator,
		defaultTenant: defaultTenant,
	}
}

func (u *PublishJobStatusChangedUseCase) Execute(
	ctx context.Context,
	command PublishJobStatusChangedCommand,
) (PublishJobStatusChangedResult, error) {
	tenantID := command.TenantID
	if tenantID == "" {
		tenantID = u.defaultTenant
	}

	newStatus := command.NewStatus
	if newStatus == "" {
		newStatus = "assigned"
	}

	event, err := domain.NewJobStatusChanged(domain.JobStatusChanged{
		EventID:           u.idGenerator.New(),
		OccurredAt:        u.clock.Now(),
		TenantID:          tenantID,
		JobID:             command.JobID,
		PreviousStatus:    command.PreviousStatus,
		NewStatus:         newStatus,
		AssignedDriverID:  command.AssignedDriverID,
		AssignedVehicleID: command.AssignedVehicleID,
		Reason:            command.Reason,
	})
	if err != nil {
		return PublishJobStatusChangedResult{}, fmt.Errorf("build job status event: %w", err)
	}

	if err := u.publisher.PublishJobStatusChanged(ctx, event); err != nil {
		return PublishJobStatusChangedResult{}, fmt.Errorf("publish job status event: %w", err)
	}

	return PublishJobStatusChangedResult{
		Published: true,
		Subject:   event.Subject(),
		EventID:   event.EventID,
	}, nil
}
