package application

import (
	"context"
	"strings"
)

type TrackingQueryUseCase struct {
	latestReader  LatestLocationReader
	historyRepo   LocationHistoryRepository
	defaultTenant string
}

func NewTrackingQueryUseCase(
	latestReader LatestLocationReader,
	historyRepo LocationHistoryRepository,
	defaultTenant string,
) *TrackingQueryUseCase {
	return &TrackingQueryUseCase{
		latestReader:  latestReader,
		historyRepo:   historyRepo,
		defaultTenant: defaultTenant,
	}
}

func (u *TrackingQueryUseCase) ListLatestLocations(_ context.Context, tenantID string) ([]LatestLocation, error) {
	resolvedTenant := u.resolveTenant(tenantID)
	locations, err := u.latestReader.List(resolvedTenant)
	if err != nil {
		return nil, err
	}
	return locations, nil
}

func (u *TrackingQueryUseCase) VehicleHistory(_ context.Context, tenantID, vehicleID string, limit int) ([]LatestLocation, error) {
	resolvedTenant := u.resolveTenant(tenantID)
	cleanVehicle := strings.TrimSpace(vehicleID)
	if cleanVehicle == "" {
		return []LatestLocation{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	history, err := u.historyRepo.History(resolvedTenant, cleanVehicle, limit)
	if err != nil {
		return nil, err
	}
	// Never nil, so the response is [] rather than null.
	if history == nil {
		history = []LatestLocation{}
	}
	return history, nil
}

func (u *TrackingQueryUseCase) resolveTenant(tenantID string) string {
	if t := strings.TrimSpace(tenantID); t != "" {
		return t
	}
	return u.defaultTenant
}
