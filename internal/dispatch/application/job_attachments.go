package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const (
	MaxJobAttachmentBytes = 3 * 1024 * 1024
	MaxJobAttachments     = 10
)

var (
	ErrAttachmentNotFound = errors.New("attachment not found")
	ErrAttachmentTooLarge = errors.New("attachment is larger than 3 MB")
	ErrAttachmentEmpty    = errors.New("attachment has no content")
	ErrTooManyAttachments = errors.New("a job can have at most 10 attachments")
)

// JobAttachmentStore keeps the bytes of files a dispatcher attaches to a job.
// The job itself only holds the file's name, type and size.
type JobAttachmentStore interface {
	Save(ctx context.Context, tenantID, jobID string, attachment domain.JobAttachment, data []byte) error
	Find(ctx context.Context, tenantID, jobID, attachmentID string) (domain.JobAttachment, []byte, bool, error)
}

type AddJobAttachmentCommand struct {
	TenantID    string
	JobID       string
	Name        string
	ContentType string
	Data        []byte
}

type OpenJobAttachmentQuery struct {
	TenantID     string
	JobID        string
	AttachmentID string
}

type OpenedJobAttachment struct {
	Attachment domain.JobAttachment
	Data       []byte
}

type JobAttachmentUseCase struct {
	jobs        JobRepository
	store       JobAttachmentStore
	clock       Clock
	idGenerator IDGenerator
}

func NewJobAttachmentUseCase(jobs JobRepository, store JobAttachmentStore, clock Clock, idGenerator IDGenerator) *JobAttachmentUseCase {
	return &JobAttachmentUseCase{jobs: jobs, store: store, clock: clock, idGenerator: idGenerator}
}

func (u *JobAttachmentUseCase) Add(ctx context.Context, command AddJobAttachmentCommand) (domain.Job, error) {
	if len(command.Data) == 0 {
		return domain.Job{}, ErrAttachmentEmpty
	}
	if len(command.Data) > MaxJobAttachmentBytes {
		return domain.Job{}, ErrAttachmentTooLarge
	}
	job, ok := u.jobs.FindByTenantAndID(command.TenantID, strings.TrimSpace(command.JobID))
	if !ok {
		return domain.Job{}, ErrJobNotFound
	}
	if len(job.Attachments) >= MaxJobAttachments {
		return domain.Job{}, ErrTooManyAttachments
	}

	attachment := domain.JobAttachment{
		ID:          u.idGenerator.New(),
		Name:        strings.TrimSpace(command.Name),
		ContentType: strings.TrimSpace(command.ContentType),
		SizeBytes:   int64(len(command.Data)),
	}
	updated, err := job.AddAttachment(attachment, u.clock.Now(), u.idGenerator.New())
	if err != nil {
		return domain.Job{}, err
	}
	if err := u.jobs.Save(updated); err != nil {
		return domain.Job{}, fmt.Errorf("save job attachment: %w", err)
	}
	if err := u.store.Save(ctx, job.TenantID, job.ID, updated.Attachments[len(updated.Attachments)-1], command.Data); err != nil {
		if rollbackErr := u.jobs.Save(job); rollbackErr != nil {
			return domain.Job{}, fmt.Errorf("store attachment: %w (rollback failed: %v)", err, rollbackErr)
		}
		return domain.Job{}, fmt.Errorf("store attachment: %w", err)
	}
	return updated, nil
}

func (u *JobAttachmentUseCase) Open(ctx context.Context, query OpenJobAttachmentQuery) (OpenedJobAttachment, error) {
	job, ok := u.jobs.FindByTenantAndID(query.TenantID, strings.TrimSpace(query.JobID))
	if !ok {
		return OpenedJobAttachment{}, ErrJobNotFound
	}
	for _, listed := range job.Attachments {
		if listed.ID != query.AttachmentID {
			continue
		}
		attachment, data, found, err := u.store.Find(ctx, job.TenantID, job.ID, listed.ID)
		if err != nil {
			return OpenedJobAttachment{}, err
		}
		if !found {
			return OpenedJobAttachment{}, ErrAttachmentNotFound
		}
		return OpenedJobAttachment{Attachment: attachment, Data: data}, nil
	}
	return OpenedJobAttachment{}, ErrAttachmentNotFound
}
