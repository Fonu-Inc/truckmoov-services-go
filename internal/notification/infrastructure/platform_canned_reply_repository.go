package infrastructure

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

var (
	_ application.CannedReplyRepository = (*PostgresWhatsAppConversationRepository)(nil)
	_ application.CannedReplyRepository = (*InMemoryCannedReplyRepository)(nil)
)

func (r *PostgresWhatsAppConversationRepository) ListCannedReplies(ctx context.Context) ([]domain.CannedReply, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, shortcut, title, body, created_by, created_at, updated_at FROM platform_canned_replies ORDER BY shortcut ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	replies := make([]domain.CannedReply, 0)
	for rows.Next() {
		var reply domain.CannedReply
		if err := rows.Scan(&reply.ID, &reply.Shortcut, &reply.Title, &reply.Body, &reply.CreatedBy, &reply.CreatedAt, &reply.UpdatedAt); err != nil {
			return nil, err
		}
		reply.CreatedAt, reply.UpdatedAt = reply.CreatedAt.UTC(), reply.UpdatedAt.UTC()
		replies = append(replies, reply)
	}
	return replies, rows.Err()
}

func (r *PostgresWhatsAppConversationRepository) SaveCannedReply(ctx context.Context, reply domain.CannedReply) (domain.CannedReply, error) {
	err := r.db.QueryRowContext(ctx, `
INSERT INTO platform_canned_replies (id, shortcut, title, body, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET shortcut = EXCLUDED.shortcut, title = EXCLUDED.title, body = EXCLUDED.body, updated_at = EXCLUDED.updated_at
RETURNING created_by, created_at, updated_at
`, reply.ID, reply.Shortcut, reply.Title, reply.Body, reply.CreatedBy, reply.CreatedAt.UTC(), reply.UpdatedAt.UTC()).
		Scan(&reply.CreatedBy, &reply.CreatedAt, &reply.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.CannedReply{}, application.ErrCannedReplyShortcutTaken
	}
	reply.CreatedAt, reply.UpdatedAt = reply.CreatedAt.UTC(), reply.UpdatedAt.UTC()
	return reply, err
}

func (r *PostgresWhatsAppConversationRepository) DeleteCannedReply(ctx context.Context, id string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM platform_canned_replies WHERE id = $1`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	deleted, err := result.RowsAffected()
	return deleted > 0, err
}

func isUniqueViolation(err error) bool {
	var coded interface{ SQLState() string }
	return errors.As(err, &coded) && coded.SQLState() == "23505"
}

type InMemoryCannedReplyRepository struct {
	mu      sync.RWMutex
	replies map[string]domain.CannedReply
}

func NewInMemoryCannedReplyRepository() *InMemoryCannedReplyRepository {
	return &InMemoryCannedReplyRepository{replies: map[string]domain.CannedReply{}}
}

func (r *InMemoryCannedReplyRepository) ListCannedReplies(context.Context) ([]domain.CannedReply, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	replies := make([]domain.CannedReply, 0, len(r.replies))
	for _, reply := range r.replies {
		replies = append(replies, reply)
	}
	sort.Slice(replies, func(i, j int) bool { return replies[i].Shortcut < replies[j].Shortcut })
	return replies, nil
}

func (r *InMemoryCannedReplyRepository) SaveCannedReply(_ context.Context, reply domain.CannedReply) (domain.CannedReply, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.replies {
		if existing.ID != reply.ID && existing.Shortcut == reply.Shortcut {
			return domain.CannedReply{}, application.ErrCannedReplyShortcutTaken
		}
	}
	if existing, ok := r.replies[reply.ID]; ok {
		reply.CreatedAt, reply.CreatedBy = existing.CreatedAt, existing.CreatedBy
	}
	r.replies[reply.ID] = reply
	return reply, nil
}

func (r *InMemoryCannedReplyRepository) DeleteCannedReply(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.replies[strings.TrimSpace(id)]
	delete(r.replies, strings.TrimSpace(id))
	return ok, nil
}
