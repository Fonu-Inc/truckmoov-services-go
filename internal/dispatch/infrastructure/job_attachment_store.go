package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

const jobAttachmentSchema = `
CREATE TABLE IF NOT EXISTS dispatch_job_attachments (
    tenant_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL,
    uploaded_at TIMESTAMPTZ NOT NULL,
    data BYTEA NOT NULL,
    PRIMARY KEY (tenant_id, job_id, id)
);
`

var _ application.JobAttachmentStore = (*PostgresJobAttachmentStore)(nil)

type PostgresJobAttachmentStore struct {
	db *sql.DB
}

func NewPostgresJobAttachmentStore(db *sql.DB) *PostgresJobAttachmentStore {
	return &PostgresJobAttachmentStore{db: db}
}

func (s *PostgresJobAttachmentStore) Save(ctx context.Context, tenantID, jobID string, attachment domain.JobAttachment, data []byte) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO dispatch_job_attachments (tenant_id, job_id, id, name, content_type, size_bytes, uploaded_at, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tenantID, jobID, attachment.ID, attachment.Name, attachment.ContentType, attachment.SizeBytes, attachment.UploadedAt, data,
	)
	return err
}

func (s *PostgresJobAttachmentStore) Find(ctx context.Context, tenantID, jobID, attachmentID string) (domain.JobAttachment, []byte, bool, error) {
	var attachment domain.JobAttachment
	var data []byte
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, content_type, size_bytes, uploaded_at, data
FROM dispatch_job_attachments WHERE tenant_id = $1 AND job_id = $2 AND id = $3`,
		tenantID, jobID, attachmentID,
	).Scan(&attachment.ID, &attachment.Name, &attachment.ContentType, &attachment.SizeBytes, &attachment.UploadedAt, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.JobAttachment{}, nil, false, nil
	}
	if err != nil {
		return domain.JobAttachment{}, nil, false, err
	}
	return attachment, data, true, nil
}

type InMemoryJobAttachmentStore struct {
	mu    sync.Mutex
	files map[string]storedAttachment
}

type storedAttachment struct {
	attachment domain.JobAttachment
	data       []byte
}

func NewInMemoryJobAttachmentStore() *InMemoryJobAttachmentStore {
	return &InMemoryJobAttachmentStore{files: map[string]storedAttachment{}}
}

func (s *InMemoryJobAttachmentStore) Save(_ context.Context, tenantID, jobID string, attachment domain.JobAttachment, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[tenantID+"|"+jobID+"|"+attachment.ID] = storedAttachment{attachment: attachment, data: append([]byte{}, data...)}
	return nil
}

func (s *InMemoryJobAttachmentStore) Find(_ context.Context, tenantID, jobID, attachmentID string) (domain.JobAttachment, []byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.files[tenantID+"|"+jobID+"|"+attachmentID]
	return stored.attachment, stored.data, ok, nil
}
