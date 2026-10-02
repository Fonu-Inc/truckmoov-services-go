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

// LateAlertStore persists which jobs already raised a late alert so a restart
// does not repeat them.
type LateAlertStore interface {
	LateAlertedJobIDs(ctx context.Context, tenantID string) (map[string]bool, error)
	MarkLateAlerted(ctx context.Context, tenantID, jobID string) error
}

type EtaMonitorUseCase struct {
	repository     JobRepository
	tenants        ActiveJobTenantLister
	alertPublisher AlertPublisher
	clock          Clock
	store          LateAlertStore
	mu             sync.Mutex
	alerted        map[string]map[string]bool
}

func NewEtaMonitorUseCase(
	repository JobRepository,
	tenants ActiveJobTenantLister,
	alertPublisher AlertPublisher,
	clock Clock,
) *EtaMonitorUseCase {
	store, _ := repository.(LateAlertStore)
	return &EtaMonitorUseCase{
		repository:     repository,
		tenants:        tenants,
		alertPublisher: alertPublisher,
		clock:          clock,
		store:          store,
		alerted:        make(map[string]map[string]bool),
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
	u.forgetIdleTenants(tenantIDs)
	for _, tenantID := range tenantIDs {
		u.checkTenant(ctx, tenantID)
	}
}

func (u *EtaMonitorUseCase) forgetIdleTenants(active []string) {
	keep := make(map[string]bool, len(active))
	for _, tenantID := range active {
		keep[tenantID] = true
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for tenantID := range u.alerted {
		if !keep[tenantID] {
			delete(u.alerted, tenantID)
		}
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

// alertedJobs returns the tenant's already-alerted job ids, from the store when
// there is one, else from memory pruned to jobs still in progress.
func (u *EtaMonitorUseCase) alertedJobs(ctx context.Context, tenantID string, jobs []domain.Job) (map[string]bool, bool) {
	if u.store != nil {
		ids, err := u.store.LateAlertedJobIDs(ctx, tenantID)
		if err != nil {
			log.Printf("eta monitor: read late alerts tenant=%s: %v", tenantID, err)
			return nil, false
		}
		return ids, true
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	pruned := make(map[string]bool)
	for _, job := range jobs {
		if u.alerted[tenantID][job.ID] {
			pruned[job.ID] = true
		}
	}
	u.alerted[tenantID] = pruned
	return pruned, true
}

func (u *EtaMonitorUseCase) markAlerted(ctx context.Context, tenantID, jobID string, seen map[string]bool) {
	seen[jobID] = true
	if u.store != nil {
		if err := u.store.MarkLateAlerted(ctx, tenantID, jobID); err != nil {
			log.Printf("eta monitor: mark late alerted tenant=%s job=%s: %v", tenantID, jobID, err)
		}
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.alerted[tenantID] = seen
}

func (u *EtaMonitorUseCase) checkTenant(ctx context.Context, tenantID string) {
	jobs := u.jobsToCheck(ctx, tenantID)
	seen, ok := u.alertedJobs(ctx, tenantID, jobs)
	if !ok {
		return
	}
	now := u.clock.Now().UTC()

	for _, job := range jobs {
		if !isActiveJob(job) || job.TimeWindow.DropoffEnd == nil {
			continue
		}
		if !now.After(job.TimeWindow.DropoffEnd.UTC()) || seen[job.ID] {
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
		u.markAlerted(ctx, tenantID, job.ID, seen)
	}
}

func isActiveJob(job domain.Job) bool {
	return job.IsActive() && job.Status != domain.StatusCreated
}
