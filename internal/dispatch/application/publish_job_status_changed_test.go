package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type fakeDispatchPublisher struct {
	event domain.JobStatusChanged
}

func (f *fakeDispatchPublisher) PublishJobStatusChanged(_ context.Context, event domain.JobStatusChanged) error {
	f.event = event
	return nil
}

type fixedClock struct{ now time.Time }

func (f fixedClock) Now() time.Time { return f.now }

type fixedIDGenerator struct{ id string }

func (f fixedIDGenerator) New() string { return f.id }

func TestPublishJobStatusChangedUseCase_Execute(t *testing.T) {
	publisher := &fakeDispatchPublisher{}
	useCase := NewPublishJobStatusChangedUseCase(
		publisher,
		fixedClock{now: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		fixedIDGenerator{id: "evt-1"},
		"default",
	)

	result, err := useCase.Execute(context.Background(), PublishJobStatusChangedCommand{
		JobID:            "job-1",
		AssignedDriverID: "driver-1",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !result.Published {
		t.Fatalf("expected published=true")
	}
	if result.Subject != "tm.default.job.status.changed" {
		t.Fatalf("unexpected subject: %s", result.Subject)
	}
	if publisher.event.NewStatus != "assigned" {
		t.Fatalf("expected default status assigned, got %s", publisher.event.NewStatus)
	}
}
