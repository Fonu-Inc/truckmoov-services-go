package infrastructure

import (
	"sort"
	"strings"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type InMemoryJobRepository struct {
	mu   sync.RWMutex
	jobs map[string]domain.Job
}

func NewInMemoryJobRepository() *InMemoryJobRepository {
	return &InMemoryJobRepository{
		jobs: make(map[string]domain.Job),
	}
}

func (r *InMemoryJobRepository) Save(job domain.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[buildJobKey(job.TenantID, job.ID)] = job
	return nil
}

func (r *InMemoryJobRepository) FindByTenantAndID(tenantID, id string) (domain.Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	job, ok := r.jobs[buildJobKey(tenantID, id)]
	return job, ok
}

func (r *InMemoryJobRepository) FindByTenant(tenantID string) []domain.Job {
	r.mu.RLock()
	defer r.mu.RUnlock()

	normalizedTenant := strings.TrimSpace(tenantID)
	results := make([]domain.Job, 0)
	for _, job := range r.jobs {
		if job.TenantID != normalizedTenant {
			continue
		}
		results = append(results, job)
	}

	// Returning jobs in creation order makes API responses deterministic for tests and debugging.
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].CreatedAt.Before(results[j].CreatedAt)
	})
	return results
}

func buildJobKey(tenantID, jobID string) string {
	return strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(jobID)
}
