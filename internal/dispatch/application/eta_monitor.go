package application

import (
	"context"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type AlertPublisher interface {
	PublishAlert(ctx context.Context, tenantID, alertType, severity, jobID, vehicleID string, details map[string]any) error
}

// ActiveJobTenantLister names the tenants that have jobs in progress, so a
// background sweep covers every real tenant rather than one configured name.
type ActiveJobTenantLister interface {
	ListTenantsWithActiveJobs(ctx context.Context) ([]string, error)
}

type EtaMonitorUseCase struct {
	repository     JobRepository
	tenants        ActiveJobTenantLister
	alertPublisher AlertPublisher
	clock          Clock
	mu             sync.Mutex
	alerted        map[string]bool
}

func NewEtaMonitorUseCase(
	repository JobRepository,
	tenants ActiveJobTenantLister,
	alertPublisher AlertPublisher,
	clock Clock,
) *EtaMonitorUseCase {
	return &EtaMonitorUseCase{
		repository:     repository,
		tenants:        tenants,
		alertPublisher: alertPublisher,
		clock:          clock,
		alerted:        make(map[string]bool),
	}
}

// Check raises a late alert for every active job of every tenant that has
// passed its drop-off window. A tenant that cannot be read is skipped, so one
// failing tenant does not silence the others.
func (u *EtaMonitorUseCase) Check(ctx context.Context) {
	tenantIDs, err := u.tenants.ListTenantsWithActiveJobs(ctx)
	if err != nil {
		return
	}
	for _, tenantID := range tenantIDs {
		u.checkTenant(ctx, tenantID)
	}
}

func (u *EtaMonitorUseCase) checkTenant(ctx context.Context, tenantID string) {
	jobs := u.repository.FindByTenant(tenantID)
	now := u.clock.Now().UTC()

	for _, job := range jobs {
		if !isActiveJob(job) || job.TimeWindow.DropoffEnd == nil {
			continue
		}
		if !now.After(job.TimeWindow.DropoffEnd.UTC()) {
			continue
		}

		key := job.TenantID + ":" + job.ID
		u.mu.Lock()
		seen := u.alerted[key]
		if !seen {
			u.alerted[key] = true
		}
		u.mu.Unlock()

		if seen {
			continue
		}

		_ = u.alertPublisher.PublishAlert(ctx, job.TenantID, "eta_breach", "high", job.ID, job.AssignedVehicleID, map[string]any{
			"deadline":     job.TimeWindow.DropoffEnd,
			"job_status":   string(job.Status),
			"driver_id":    job.AssignedDriverID,
			"dropoff_addr": job.Dropoff.Address,
		})
	}
}

func isActiveJob(job domain.Job) bool {
	return job.Status == domain.StatusEnRoute || job.Status == domain.StatusAssigned || job.Status == domain.StatusArrived
}
