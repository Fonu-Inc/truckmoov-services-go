package application

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/tracking/domain"
)

type fakeTrackingPublisher struct {
	location domain.LocationUpdate
}

func (f *fakeTrackingPublisher) PublishLocationUpdate(_ context.Context, event domain.LocationUpdate) error {
	f.location = event
	return nil
}

func (f *fakeTrackingPublisher) PublishAlertTriggered(_ context.Context, _ domain.AlertTriggered) error {
	return nil
}

type fakeLatestLocationRepository struct {
	entry LatestLocation
}

func (f *fakeLatestLocationRepository) Save(location LatestLocation) error {
	f.entry = location
	return nil
}

type fixedTrackingClock struct{ now time.Time }

func (f fixedTrackingClock) Now() time.Time { return f.now }

type fixedTrackingIDGenerator struct{ value string }

func (f fixedTrackingIDGenerator) New(_ string) string { return f.value }

func TestIngestLocationUseCase_Execute(t *testing.T) {
	publisher := &fakeTrackingPublisher{}
	repository := &fakeLatestLocationRepository{}
	useCase := NewIngestLocationUseCase(
		publisher,
		repository,
		fixedTrackingClock{now: time.Date(2026, 2, 2, 11, 0, 0, 0, time.UTC)},
		fixedTrackingIDGenerator{value: "evt-123"},
		"default",
	)

	result, err := useCase.Execute(context.Background(), IngestLocationCommand{
		VehicleID: "vehicle-1",
		DeviceID:  "device-1",
		Latitude:  6.5,
		Longitude: 3.4,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !result.Published {
		t.Fatalf("expected published=true")
	}
	if result.Subject != "tm.default.vehicle.location.updated" {
		t.Fatalf("unexpected subject: %s", result.Subject)
	}
	if repository.entry.VehicleID != "vehicle-1" {
		t.Fatalf("expected stored vehicle id, got %s", repository.entry.VehicleID)
	}
}
