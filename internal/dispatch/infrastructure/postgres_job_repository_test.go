package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	"github.com/truckmoov/services-go/pkg/config"
)

func TestDecodeStoredJob_NormalizesLegacyPriority(t *testing.T) {
	payload := []byte(`{
		"id":"job-1",
		"tenant_id":"tenant-1",
		"pickup":{"address":"Apapa"},
		"dropoff":{"address":"Lekki"},
		"priority":"critical",
		"status":"created",
		"created_at":"2026-04-01T10:00:00Z",
		"updated_at":"2026-04-01T10:00:00Z"
	}`)

	job, err := decodeStoredJob(payload)
	if err != nil {
		t.Fatalf("decodeStoredJob returned error: %v", err)
	}
	if job.Priority != domain.PriorityUrgent {
		t.Fatalf("expected legacy critical priority to normalize to %q, got %q", domain.PriorityUrgent, job.Priority)
	}
}

func newTestPostgresRepo(t *testing.T) *PostgresJobRepository {
	t.Helper()

	cfg := config.Load("DISPATCH", "3005")
	db, err := sql.Open("pgx", cfg.PostgresDSN())
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}

	repo := NewPostgresJobRepository(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Skipf("postgres schema setup failed: %v", err)
	}
	return repo
}

func TestPostgresJobRepository_Delete_RemovesJob(t *testing.T) {
	repo := newTestPostgresRepo(t)
	tenantID := "tenant-pg-delete"
	id := fmt.Sprintf("job-delete-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = repo.Delete(tenantID, id) })

	job := newTestJob(t, tenantID, id)
	if err := repo.Save(job); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := repo.Delete(tenantID, id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, ok := repo.FindByTenantAndID(tenantID, id); ok {
		t.Fatalf("expected job to be gone after delete")
	}
}

func TestPostgresJobRepository_Delete_WrongTenantIsNoOp(t *testing.T) {
	repo := newTestPostgresRepo(t)
	tenantID := "tenant-pg-a"
	wrongTenantID := "tenant-pg-b"
	id := fmt.Sprintf("job-tenant-isolation-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = repo.Delete(tenantID, id) })

	job := newTestJob(t, tenantID, id)
	if err := repo.Save(job); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := repo.Delete(wrongTenantID, id); err != nil {
		t.Fatalf("delete under wrong tenant: %v", err)
	}

	if _, ok := repo.FindByTenantAndID(tenantID, id); !ok {
		t.Fatalf("expected job to remain when deleted under wrong tenant")
	}
}
