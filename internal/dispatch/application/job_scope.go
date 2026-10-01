package application

import "github.com/truckmoov/services-go/internal/dispatch/domain"

// JobScope limits a caller to jobs on the vehicles they own. A nil scope is
// the whole tenant; an empty one sees nothing, including unassigned jobs.
type JobScope struct {
	vehicles map[string]bool
}

func NewVehicleScope(vehicleIDs []string) *JobScope {
	scope := &JobScope{vehicles: make(map[string]bool, len(vehicleIDs))}
	for _, id := range vehicleIDs {
		if id != "" {
			scope.vehicles[id] = true
		}
	}
	return scope
}

func (s *JobScope) AllowsVehicle(vehicleID string) bool {
	return s == nil || s.vehicles[vehicleID]
}

func (s *JobScope) Allows(job domain.Job) bool {
	return s.AllowsVehicle(job.AssignedVehicleID)
}
