package infrastructure

import (
	"testing"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func floatPtr(v float64) *float64 { return &v }

func TestToVroomRequest_AssignsPickupAndDeliveryIDs(t *testing.T) {
	job := domain.Job{
		ID:       "job-1",
		TenantID: "default",
		Pickup: domain.JobStop{
			Address:   "Pickup",
			Latitude:  floatPtr(6.45),
			Longitude: floatPtr(3.35),
		},
		Dropoff: domain.JobStop{
			Address:   "Dropoff",
			Latitude:  floatPtr(6.44),
			Longitude: floatPtr(3.48),
		},
		EstimatedServiceMinutes: 5,
	}

	req, _, err := toVroomRequest([]domain.Job{job}, application.OptimizeRouteCommand{
		Origin: application.RouteLocation{Latitude: 6.50, Longitude: 3.30},
		Vehicle: application.RouteVehicleProfile{
			VehicleType: "car",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Shipments) != 1 {
		t.Fatalf("expected 1 shipment, got %d", len(req.Shipments))
	}

	shipment := req.Shipments[0]
	if shipment.Pickup.ID != 1 {
		t.Fatalf("expected pickup id 1, got %d", shipment.Pickup.ID)
	}
	if shipment.Delivery.ID != 2 {
		t.Fatalf("expected delivery id 2, got %d", shipment.Delivery.ID)
	}
}
