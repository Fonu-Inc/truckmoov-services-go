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

func TestDecodeStoredJob_LegacyJobHasEmptyListsNotNull(t *testing.T) {
	payload := []byte(`{"id":"job-1","tenant_id":"tenant-1","pickup":{"address":"Apapa"},"dropoff":{"address":"Lekki"},"status":"created","created_at":"2026-04-01T10:00:00Z","updated_at":"2026-04-01T10:00:00Z"}`)

	job, err := decodeStoredJob(payload)
	if err != nil {
		t.Fatalf("decodeStoredJob returned error: %v", err)
	}
	if job.Attachments == nil {
		t.Fatal("a job stored before attachments existed must serialize attachments as [], not null")
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

func TestDecodeStoredJob_CreatedBy(t *testing.T) {
	base := `"id":"job-1","tenant_id":"tenant-1","pickup":{"address":"Apapa"},"dropoff":{"address":"Lekki"},"status":"created","created_at":"2026-04-01T10:00:00Z","updated_at":"2026-04-01T10:00:00Z"`

	legacy, err := decodeStoredJob([]byte(`{` + base + `}`))
	if err != nil || legacy.CreatedBy != nil {
		t.Fatalf("legacy job: expected nil created_by, got %v (err %v)", legacy.CreatedBy, err)
	}
	recorded, err := decodeStoredJob([]byte(`{` + base + `,"created_by":"oo-a"}`))
	if err != nil || recorded.CreatedBy == nil || *recorded.CreatedBy != "oo-a" {
		t.Fatalf("recorded job: expected created_by oo-a, got %v (err %v)", recorded.CreatedBy, err)
	}
}

func TestPostgresJobRepository_PersistsCreatedBy(t *testing.T) {
	repo := newTestPostgresRepo(t)
	tenantID := "tenant-pg-created-by"
	id := fmt.Sprintf("job-created-by-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = repo.Delete(tenantID, id) })

	job := newTestJob(t, tenantID, id)
	creator := "oo-a"
	job.CreatedBy = &creator
	if err := repo.Save(job); err != nil {
		t.Fatalf("save: %v", err)
	}

	var column sql.NullString
	if err := repo.db.QueryRow(`SELECT created_by FROM dispatch_jobs WHERE tenant_id = $1 AND id = $2`, tenantID, id).Scan(&column); err != nil {
		t.Fatalf("read created_by column: %v", err)
	}
	stored, ok := repo.FindByTenantAndID(tenantID, id)
	if column.String != creator || !ok || stored.CreatedBy == nil || *stored.CreatedBy != creator {
		t.Fatalf("expected created_by %q in column and payload, got %q / %v", creator, column.String, stored.CreatedBy)
	}
}

func TestPostgresJobRepository_PersistsDeliveredAtAndCountsActive(t *testing.T) {
	repo := newTestPostgresRepo(t)
	tenantID := fmt.Sprintf("tenant-pg-delivered-%d", time.Now().UnixNano())
	deliveredID, activeID := "job-delivered", "job-active"
	t.Cleanup(func() { _ = repo.Delete(tenantID, deliveredID); _ = repo.Delete(tenantID, activeID) })

	delivered := newTestJob(t, tenantID, deliveredID)
	deliveredAt := time.Date(2026, 4, 5, 9, 0, 0, 0, time.UTC)
	delivered.Status = domain.StatusDelivered
	delivered.DeliveredAt = &deliveredAt
	active := newTestJob(t, tenantID, activeID)
	active.Status = domain.StatusEnRoute
	for _, job := range []domain.Job{delivered, active} {
		if err := repo.Save(job); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	var column sql.NullTime
	if err := repo.db.QueryRow(`SELECT delivered_at FROM dispatch_jobs WHERE tenant_id = $1 AND id = $2`, tenantID, deliveredID).Scan(&column); err != nil {
		t.Fatalf("read delivered_at column: %v", err)
	}
	stored, ok := repo.FindByTenantAndID(tenantID, deliveredID)
	if !column.Valid || !column.Time.Equal(deliveredAt) || !ok || stored.DeliveredAt == nil || !stored.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("expected delivered_at %v in column and payload, got %v / %v", deliveredAt, column, stored.DeliveredAt)
	}

	count, err := repo.CountActive(context.Background(), tenantID)
	if err != nil || count != 1 {
		t.Fatalf("expected one active job, got %d (err %v)", count, err)
	}
	tenants, err := repo.ListTenantsWithActiveJobs(context.Background())
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	found := false
	for _, id := range tenants {
		found = found || id == tenantID
	}
	if !found {
		t.Fatalf("expected %s among tenants with active jobs, got %v", tenantID, tenants)
	}
}
