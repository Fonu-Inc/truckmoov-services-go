package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrDriverNotFound            = errors.New("driver not found")
	ErrVehicleNotFound           = errors.New("vehicle not found")
	ErrFleetUnavailable          = errors.New("fleet service unavailable")
	ErrVehicleMaintenanceBlocked = errors.New("vehicle blocked by maintenance")
)

// MaintenanceGate is an optional FleetDirectory extra: the reason the tenant's
// maintenance rules block a vehicle from dispatch, or "" when it may go.
type MaintenanceGate interface {
	MaintenanceBlockReason(ctx context.Context, tenantID, vehicleID string) (string, error)
}

// verifyMaintenance applies only when the vehicle changes, so a job already on
// a vehicle is not trapped by a rule saved after it was assigned.
func verifyMaintenance(ctx context.Context, fleet FleetDirectory, tenantID, vehicleID, currentVehicleID string) error {
	gate, ok := fleet.(MaintenanceGate)
	vehicleID = strings.TrimSpace(vehicleID)
	if !ok || vehicleID == "" || vehicleID == strings.TrimSpace(currentVehicleID) {
		return nil
	}
	reason, err := gate.MaintenanceBlockReason(ctx, tenantID, vehicleID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFleetUnavailable, err)
	}
	if reason != "" {
		return fmt.Errorf("%w: %s", ErrVehicleMaintenanceBlocked, reason)
	}
	return nil
}

// FleetDirectory answers whether a driver or vehicle exists in a tenant. A
// false with no error means fleet answered no; an error means it could not say.
type FleetDirectory interface {
	DriverExists(ctx context.Context, tenantID, driverID string) (bool, error)
	VehicleExists(ctx context.Context, tenantID, vehicleID string) (bool, error)
}

type existsFunc func(ctx context.Context, tenantID, id string) (bool, error)

// verifyAssignees refuses ids fleet does not know. Without a directory
// (tests, in-memory runs) ids are trusted.
func verifyAssignees(ctx context.Context, fleet FleetDirectory, tenantID, driverID, vehicleID string) error {
	if fleet == nil {
		return nil
	}
	if err := verifyExists(ctx, fleet.DriverExists, tenantID, driverID, ErrDriverNotFound); err != nil {
		return err
	}
	return verifyExists(ctx, fleet.VehicleExists, tenantID, vehicleID, ErrVehicleNotFound)
}

func verifyExists(ctx context.Context, exists existsFunc, tenantID, id string, notFound error) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	found, err := exists(ctx, tenantID, id)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFleetUnavailable, err)
	}
	if !found {
		return notFound
	}
	return nil
}
