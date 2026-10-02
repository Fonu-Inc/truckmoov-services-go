package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type memoryAttachmentStore struct {
	files map[string][]byte
}

func (s *memoryAttachmentStore) Save(_ context.Context, tenantID, jobID string, attachment domain.JobAttachment, data []byte) error {
	s.files[tenantID+"|"+jobID+"|"+attachment.ID] = data
	return nil
}

func (s *memoryAttachmentStore) Find(_ context.Context, tenantID, jobID, attachmentID string) (domain.JobAttachment, []byte, bool, error) {
	data, ok := s.files[tenantID+"|"+jobID+"|"+attachmentID]
	return domain.JobAttachment{ID: attachmentID, Name: "file"}, data, ok, nil
}

func newAttachmentUseCase(t *testing.T) (*JobAttachmentUseCase, *lifecycleRepository) {
	t.Helper()
	repository := newLifecycleRepository()
	now := time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)
	job, err := domain.NewJob(domain.Job{
		ID: "job-1", TenantID: "tenant-a", Pickup: domain.JobStop{Address: "A"}, Dropoff: domain.JobStop{Address: "B"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_ = repository.Save(job)
	store := &memoryAttachmentStore{files: map[string][]byte{}}
	return NewJobAttachmentUseCase(repository, store, lifecycleClock{now: now}, &lifecycleID{}), repository
}

func TestJobAttachment_AddKeepsMetadataOnTheJobAndOpensTheBytes(t *testing.T) {
	useCase, repository := newAttachmentUseCase(t)

	job, err := useCase.Add(context.Background(), AddJobAttachmentCommand{TenantID: "tenant-a", JobID: "job-1", Name: "bol.pdf", ContentType: "application/pdf", Data: []byte("pdf")})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	stored, _ := repository.FindByTenantAndID("tenant-a", "job-1")
	if len(stored.Attachments) != 1 || stored.Attachments[0].Name != "bol.pdf" || stored.Attachments[0].SizeBytes != 3 || len(job.Attachments) != 1 {
		t.Fatalf("unexpected attachments %+v", stored.Attachments)
	}

	opened, err := useCase.Open(context.Background(), OpenJobAttachmentQuery{TenantID: "tenant-a", JobID: "job-1", AttachmentID: stored.Attachments[0].ID})
	if err != nil || string(opened.Data) != "pdf" {
		t.Fatalf("open: %v %q", err, opened.Data)
	}
}

func TestJobAttachment_RefusesOtherTenantsEmptyOversizedAndExcessFiles(t *testing.T) {
	useCase, _ := newAttachmentUseCase(t)
	ctx := context.Background()

	if _, err := useCase.Add(ctx, AddJobAttachmentCommand{TenantID: "tenant-b", JobID: "job-1", Name: "x", Data: []byte("x")}); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected not found for another tenant, got %v", err)
	}
	if _, err := useCase.Add(ctx, AddJobAttachmentCommand{TenantID: "tenant-a", JobID: "job-1", Name: "x"}); !errors.Is(err, ErrAttachmentEmpty) {
		t.Fatalf("expected empty refusal, got %v", err)
	}
	big := make([]byte, MaxJobAttachmentBytes+1)
	if _, err := useCase.Add(ctx, AddJobAttachmentCommand{TenantID: "tenant-a", JobID: "job-1", Name: "x", Data: big}); !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("expected size refusal, got %v", err)
	}
	for i := 0; i < MaxJobAttachments; i++ {
		if _, err := useCase.Add(ctx, AddJobAttachmentCommand{TenantID: "tenant-a", JobID: "job-1", Name: "x", Data: []byte("x")}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := useCase.Add(ctx, AddJobAttachmentCommand{TenantID: "tenant-a", JobID: "job-1", Name: "x", Data: []byte("x")}); !errors.Is(err, ErrTooManyAttachments) {
		t.Fatalf("expected count refusal, got %v", err)
	}
	if _, err := useCase.Open(ctx, OpenJobAttachmentQuery{TenantID: "tenant-b", JobID: "job-1", AttachmentID: "a"}); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected not found opening under another tenant, got %v", err)
	}
	if _, err := useCase.Open(ctx, OpenJobAttachmentQuery{TenantID: "tenant-a", JobID: "job-1", AttachmentID: "missing"}); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("expected attachment not found, got %v", err)
	}
}
