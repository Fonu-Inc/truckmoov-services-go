package application

import (
	"context"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type AlertPublisher interface {
	PublishAlert(ctx context.Context, tenantID, alertType, severity, jobID, vehicleID string, details map[string]any) error
}

type EtaMonitorUseCase struct {
	repository     JobRepository
	alertPublisher AlertPublisher
	clock          Clock
	defaultTenant  string
	mu             sync.Mutex
	alerted        map[string]bool
}

func NewEtaMonitorUseCase(
	repository JobRepository,
	alertPublisher AlertPublisher,
	clock Clock,
	defaultTenant string,
) *EtaMonitorUseCase {
	return &EtaMonitorUseCase{
		repository:     repository,
		alertPublisher: alertPublisher,
		clock:          clock,
		defaultTenant:  defaultTenant,
		alerted:        make(map[string]bool),
	}
}

func (u *EtaMonitorUseCase) Check(ctx context.Context) {
	jobs := u.repository.FindByTenant(u.defaultTenant)
	now := u.clock.Now().UTC()

	for _, job := range jobs {
		if !isActiveJob(job) || job.TimeWindow.DropoffEnd == nil {
			continue
		}
		if !now.After(job.TimeWindow.DropoffEnd.UTC()) {
			continue
		}

		u.mu.Lock()
		seen := u.alerted[job.ID]
		if !seen {
			u.alerted[job.ID] = true
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
