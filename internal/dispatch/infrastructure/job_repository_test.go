package infrastructure

import (
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

	if _, ok := repo.FindByTenantAndID(job.TenantID, job.ID); ok {
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

	if _, ok := repo.FindByTenantAndID(job.TenantID, job.ID); !ok {
		t.Fatalf("expected unrelated job to remain after no-op delete")
	}
}
