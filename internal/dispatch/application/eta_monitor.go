package application

import (
	"context"
	"log"
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

// InProgressJobSource reads only a tenant's jobs that are underway, so a sweep
// does not decode the tenant's whole history.
type InProgressJobSource interface {
	FindInProgressByTenant(ctx context.Context, tenantID string) ([]domain.Job, error)
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

func (u *EtaMonitorUseCase) jobsToCheck(ctx context.Context, tenantID string) []domain.Job {
	source, ok := u.repository.(InProgressJobSource)
	if !ok {
		jobs, err := u.repository.FindByTenant(tenantID)
		if err != nil {
			log.Printf("eta monitor: list jobs tenant=%s: %v", tenantID, err)
			return nil
		}
		return jobs
	}
	jobs, err := source.FindInProgressByTenant(ctx, tenantID)
	if err != nil {
		return nil
	}
	return jobs
}

func (u *EtaMonitorUseCase) checkTenant(ctx context.Context, tenantID string) {
	jobs := u.jobsToCheck(ctx, tenantID)
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
		u.mu.Unlock()
		if seen {
			continue
		}

		err := u.alertPublisher.PublishAlert(ctx, job.TenantID, "eta_breach", "high", job.ID, job.AssignedVehicleID, map[string]any{
			"deadline":     job.TimeWindow.DropoffEnd,
			"job_status":   string(job.Status),
			"driver_id":    job.AssignedDriverID,
			"dropoff_addr": job.Dropoff.Address,
		})
		if err != nil {
			continue
		}
		u.mu.Lock()
		u.alerted[key] = true
		u.mu.Unlock()
	}
}

func isActiveJob(job domain.Job) bool {
	return job.IsActive() && job.Status != domain.StatusCreated
}
