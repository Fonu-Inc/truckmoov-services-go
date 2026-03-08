package grpc

import "context"

type Location struct {
	VehicleID string
	Latitude  float64
	Longitude float64
}

type TrackingClient interface {
	GetLatestLocation(ctx context.Context, vehicleID string) (Location, error)
	ComputeETA(ctx context.Context, jobID, vehicleID, destination string) (int64, error)
}

type StubTrackingClient struct{}

func NewStubTrackingClient() *StubTrackingClient {
	return &StubTrackingClient{}
}

func (s *StubTrackingClient) GetLatestLocation(_ context.Context, vehicleID string) (Location, error) {
	return Location{VehicleID: vehicleID, Latitude: 0, Longitude: 0}, nil
}

func (s *StubTrackingClient) ComputeETA(_ context.Context, _ string, _ string, _ string) (int64, error) {
	return 900, nil
}
