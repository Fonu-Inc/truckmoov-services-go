package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
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

func (r *InMemoryJobRepository) FindByTenantAndID(tenantID, id string) (domain.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	job, ok := r.jobs[buildJobKey(tenantID, id)]
	if !ok {
		return domain.Job{}, domain.ErrJobNotFound
	}
	return job, nil
}

func (r *InMemoryJobRepository) FindByTenant(tenantID string) ([]domain.Job, error) {
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
	return results, nil
}

func (r *InMemoryJobRepository) ListTenantsWithActiveJobs(_ context.Context) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := map[string]bool{}
	tenants := make([]string, 0)
	for _, job := range r.jobs {
		if job.IsActive() && job.Status != domain.StatusCreated && !seen[job.TenantID] {
			seen[job.TenantID] = true
			tenants = append(tenants, job.TenantID)
		}
	}
	sort.Strings(tenants)
	return tenants, nil
}

func (r *InMemoryJobRepository) CountActive(_ context.Context, tenantID string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, job := range r.jobs {
		if job.TenantID == strings.TrimSpace(tenantID) && job.IsActive() {
			count++
		}
	}
	return count, nil
}

func (r *InMemoryJobRepository) Delete(tenantID, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, buildJobKey(tenantID, id))
	return nil
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

func (r *RedisJobRepository) FindByTenantAndID(tenantID, id string) (domain.Job, error) {
	payload, ok, err := r.store.Get(redisJobKey(tenantID, id))
	if err != nil {
		return domain.Job{}, fmt.Errorf("read job: %w", err)
	}
	if !ok {
		return domain.Job{}, domain.ErrJobNotFound
	}

	var job domain.Job
	if err := json.Unmarshal(payload, &job); err != nil {
		return domain.Job{}, fmt.Errorf("decode job: %w", err)
	}
	return job, nil
}

func (r *RedisJobRepository) FindByTenant(tenantID string) ([]domain.Job, error) {
	jobIDs, err := r.store.SMembers(redisJobIndexKey(tenantID))
	if err != nil {
		return nil, fmt.Errorf("list tenant job ids: %w", err)
	}

	results := make([]domain.Job, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		job, err := r.FindByTenantAndID(tenantID, jobID)
		if errors.Is(err, domain.ErrJobNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		results = append(results, job)
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].CreatedAt.Before(results[j].CreatedAt)
	})
	return results, nil
}

func (r *RedisJobRepository) Delete(tenantID, id string) error {
	if err := r.store.Del(redisJobKey(tenantID, id)); err != nil {
		return err
	}
	return r.store.SRem(redisJobIndexKey(tenantID), strings.TrimSpace(id))
}

func redisJobKey(tenantID, jobID string) string {
	return "dispatch:job:" + buildJobKey(tenantID, jobID)
}

func redisJobIndexKey(tenantID string) string {
	return "dispatch:job-index:" + strings.TrimSpace(tenantID)
}
