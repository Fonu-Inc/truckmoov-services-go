package application

import "github.com/truckmoov/services-go/internal/dispatch/domain"

// JobScope limits an owner operator to jobs on the vehicles they own and jobs
// they created. A nil scope is the whole tenant; an empty one sees nothing,
// including unassigned jobs someone else created.
type JobScope struct {
	vehicles  map[string]bool
	creatorID string
}

func NewOwnerOperatorScope(ownerOperatorID string, vehicleIDs []string) *JobScope {
	scope := &JobScope{vehicles: make(map[string]bool, len(vehicleIDs)), creatorID: ownerOperatorID}
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
	return s.AllowsVehicle(job.AssignedVehicleID) || s.created(job)
}

func (s *JobScope) created(job domain.Job) bool {
	return s.creatorID != "" && job.CreatedBy != nil && *job.CreatedBy == s.creatorID
}
