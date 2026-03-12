package infrastructure

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
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

type RedisJobRepository struct {
	store *redisstore.Store
}

func NewRedisJobRepository(store *redisstore.Store) *RedisJobRepository {
	return &RedisJobRepository{store: store}
}

func (r *RedisJobRepository) Save(job domain.Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	if err := r.store.Set(redisJobKey(job.TenantID, job.ID), payload); err != nil {
		return err
	}
	return r.store.SAdd(redisJobIndexKey(job.TenantID), strings.TrimSpace(job.ID))
}

func (r *RedisJobRepository) FindByTenantAndID(tenantID, id string) (domain.Job, bool) {
	payload, ok, err := r.store.Get(redisJobKey(tenantID, id))
	if err != nil || !ok {
		return domain.Job{}, false
	}

	var job domain.Job
	if err := json.Unmarshal(payload, &job); err != nil {
		return domain.Job{}, false
	}
	return job, true
}

func (r *RedisJobRepository) FindByTenant(tenantID string) []domain.Job {
	jobIDs, err := r.store.SMembers(redisJobIndexKey(tenantID))
	if err != nil {
		return []domain.Job{}
	}

	results := make([]domain.Job, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		job, ok := r.FindByTenantAndID(tenantID, jobID)
		if !ok {
			continue
		}
		results = append(results, job)
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].CreatedAt.Before(results[j].CreatedAt)
	})
	return results
}

func redisJobKey(tenantID, jobID string) string {
	return "dispatch:job:" + buildJobKey(tenantID, jobID)
}

func redisJobIndexKey(tenantID string) string {
	return "dispatch:job-index:" + strings.TrimSpace(tenantID)
}
