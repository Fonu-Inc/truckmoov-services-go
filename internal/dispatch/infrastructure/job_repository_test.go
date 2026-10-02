package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func newTestJob(t *testing.T, tenantID, id string) domain.Job {
	t.Helper()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID:        id,
		TenantID:  tenantID,
		Pickup:    domain.JobStop{Address: "Apapa Port"},
		Dropoff:   domain.JobStop{Address: "Ikeja Industrial Estate"},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	return job
}

func TestInMemoryJobRepository_Delete_RemovesJob(t *testing.T) {
	repo := NewInMemoryJobRepository()
	job := newTestJob(t, "tenant-1", "job-1")
	if err := repo.Save(job); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := repo.Delete(job.TenantID, job.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := repo.FindByTenantAndID(job.TenantID, job.ID); err == nil {
		t.Fatalf("expected job to be gone after delete")
	}
}

func TestInMemoryJobRepository_Delete_NonExistentIsNoOp(t *testing.T) {
	repo := NewInMemoryJobRepository()
	job := newTestJob(t, "tenant-1", "job-1")
	if err := repo.Save(job); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := repo.Delete("tenant-1", "does-not-exist"); err != nil {
		t.Fatalf("delete non-existent: %v", err)
	}

	if _, err := repo.FindByTenantAndID(job.TenantID, job.ID); err != nil {
		t.Fatalf("expected unrelated job to remain after no-op delete")
	}
}

func TestInMemoryJobRepository_KeepsCreatedBy(t *testing.T) {
	repo := NewInMemoryJobRepository()
	job := newTestJob(t, "tenant-1", "job-1")
	creator := "oo-a"
	job.CreatedBy = &creator
	if err := repo.Save(job); err != nil {
		t.Fatalf("save: %v", err)
	}

	stored, err := repo.FindByTenantAndID(job.TenantID, job.ID)
	if err != nil || stored.CreatedBy == nil || *stored.CreatedBy != creator {
		t.Fatalf("expected created_by %q, got %v", creator, stored.CreatedBy)
	}
}

func TestInMemoryJobRepository_ListsTenantsWithActiveJobsAndCountsActive(t *testing.T) {
	repo := NewInMemoryJobRepository()
	save := func(tenantID, id string, status domain.JobStatus) {
		job := newTestJob(t, tenantID, id)
		job.Status = status
		if err := repo.Save(job); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	save("tenant-a", "j1", domain.StatusEnRoute)
	save("tenant-a", "j2", domain.StatusCreated)
	save("tenant-b", "j3", domain.StatusAssigned)
	save("tenant-c", "j4", domain.StatusDelivered)
	save("tenant-d", "j5", domain.StatusCreated)

	tenants, err := repo.ListTenantsWithActiveJobs(context.Background())
	if err != nil || len(tenants) != 2 || tenants[0] != "tenant-a" || tenants[1] != "tenant-b" {
		t.Fatalf("expected tenant-a and tenant-b, got %v (err %v)", tenants, err)
	}

	count, err := repo.CountActive(context.Background(), "tenant-a")
	if err != nil || count != 2 {
		t.Fatalf("expected 2 active jobs for tenant-a, got %d (err %v)", count, err)
	}
	if count, _ := repo.CountActive(context.Background(), "tenant-c"); count != 0 {
		t.Fatalf("a delivered job is not active, got %d", count)
	}
}
