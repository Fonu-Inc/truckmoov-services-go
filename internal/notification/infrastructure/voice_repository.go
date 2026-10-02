package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

var (
	_ application.VoiceRepository = (*PostgresVoiceRepository)(nil)
	_ application.VoiceRepository = (*InMemoryVoiceRepository)(nil)
)

const voiceSchema = `
CREATE TABLE IF NOT EXISTS platform_ivr_prompts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    category TEXT NOT NULL,
    translations JSONB NOT NULL DEFAULT '{}'::jsonb,
    key_press TEXT NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS platform_voice_calls (
    id TEXT PRIMARY KEY,
    provider_call_id TEXT NOT NULL DEFAULT '',
    direction TEXT NOT NULL,
    from_number TEXT NOT NULL DEFAULT '',
    to_number TEXT NOT NULL DEFAULT '',
    contact_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'en',
    started_at TIMESTAMPTZ NOT NULL,
    answered_at TIMESTAMPTZ NULL,
    ended_at TIMESTAMPTZ NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    recording_url TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_platform_voice_calls_started ON platform_voice_calls (started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_platform_voice_calls_provider ON platform_voice_calls (provider_call_id) WHERE provider_call_id <> '';
`

const (
	promptColumns = `id, name, category, translations, key_press, is_active, created_by, created_at, updated_at`
	callColumns   = `id, provider_call_id, direction, from_number, to_number, contact_name, status, language, started_at, answered_at, ended_at, duration_seconds, recording_url, notes, created_by, created_at, updated_at`
)

type PostgresVoiceRepository struct{ db *sql.DB }

func NewPostgresVoiceRepository(db *sql.DB) *PostgresVoiceRepository {
	return &PostgresVoiceRepository{db: db}
}

func (r *PostgresVoiceRepository) EnsureSchema(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, voiceSchema); err != nil {
		return fmt.Errorf("ensure voice schema: %w", err)
	}
	return nil
}

func scanPrompt(scanner sqlScanner) (domain.IVRPrompt, error) {
	var prompt domain.IVRPrompt
	var translations []byte
	if err := scanner.Scan(&prompt.ID, &prompt.Name, &prompt.Category, &translations, &prompt.KeyPress, &prompt.IsActive, &prompt.CreatedBy, &prompt.CreatedAt, &prompt.UpdatedAt); err != nil {
		return domain.IVRPrompt{}, err
	}
	_ = json.Unmarshal(translations, &prompt.Translations)
	if prompt.Translations == nil {
		prompt.Translations = map[string]string{}
	}
	prompt.CreatedAt, prompt.UpdatedAt = prompt.CreatedAt.UTC(), prompt.UpdatedAt.UTC()
	return prompt, nil
}

func (r *PostgresVoiceRepository) ListPrompts(ctx context.Context) ([]domain.IVRPrompt, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+promptColumns+` FROM platform_ivr_prompts ORDER BY category ASC, lower(name) ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	prompts := []domain.IVRPrompt{}
	for rows.Next() {
		prompt, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		prompts = append(prompts, prompt)
	}
	return prompts, rows.Err()
}

func (r *PostgresVoiceRepository) FindPrompt(ctx context.Context, id string) (domain.IVRPrompt, bool, error) {
	prompt, err := scanPrompt(r.db.QueryRowContext(ctx, `SELECT `+promptColumns+` FROM platform_ivr_prompts WHERE id = $1`, strings.TrimSpace(id)))
	if err == sql.ErrNoRows {
		return domain.IVRPrompt{}, false, nil
	}
	return prompt, err == nil, err
}

func (r *PostgresVoiceRepository) SavePrompt(ctx context.Context, prompt domain.IVRPrompt) (domain.IVRPrompt, error) {
	translations, err := json.Marshal(prompt.Translations)
	if err != nil {
		return domain.IVRPrompt{}, err
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO platform_ivr_prompts (`+promptColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, category = EXCLUDED.category, translations = EXCLUDED.translations,
    key_press = EXCLUDED.key_press, is_active = EXCLUDED.is_active, updated_at = EXCLUDED.updated_at
`, prompt.ID, prompt.Name, prompt.Category, translations, prompt.KeyPress, prompt.IsActive, prompt.CreatedBy, prompt.CreatedAt.UTC(), prompt.UpdatedAt.UTC())
	return prompt, err
}

func (r *PostgresVoiceRepository) DeletePrompt(ctx context.Context, id string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM platform_ivr_prompts WHERE id = $1`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	deleted, err := result.RowsAffected()
	return deleted > 0, err
}

func scanCall(scanner sqlScanner) (domain.VoiceCall, error) {
	var call domain.VoiceCall
	var answered, ended sql.NullTime
	if err := scanner.Scan(&call.ID, &call.ProviderCallID, &call.Direction, &call.FromNumber, &call.ToNumber, &call.ContactName, &call.Status, &call.Language,
		&call.StartedAt, &answered, &ended, &call.DurationSeconds, &call.RecordingURL, &call.Notes, &call.CreatedBy, &call.CreatedAt, &call.UpdatedAt); err != nil {
		return domain.VoiceCall{}, err
	}
	call.AnsweredAt, call.EndedAt = timePtr(answered), timePtr(ended)
	call.StartedAt, call.CreatedAt, call.UpdatedAt = call.StartedAt.UTC(), call.CreatedAt.UTC(), call.UpdatedAt.UTC()
	return call, nil
}

func (r *PostgresVoiceRepository) SaveCall(ctx context.Context, call domain.VoiceCall) (domain.VoiceCall, error) {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO platform_voice_calls (`+callColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, answered_at = EXCLUDED.answered_at, ended_at = EXCLUDED.ended_at,
    duration_seconds = EXCLUDED.duration_seconds, recording_url = EXCLUDED.recording_url, notes = EXCLUDED.notes,
    contact_name = EXCLUDED.contact_name, updated_at = EXCLUDED.updated_at
`, call.ID, call.ProviderCallID, call.Direction, call.FromNumber, call.ToNumber, call.ContactName, call.Status, call.Language,
		call.StartedAt.UTC(), nullableTime(call.AnsweredAt), nullableTime(call.EndedAt), call.DurationSeconds, call.RecordingURL, call.Notes, call.CreatedBy, call.CreatedAt.UTC(), call.UpdatedAt.UTC())
	return call, err
}

func (r *PostgresVoiceRepository) FindCall(ctx context.Context, id string) (domain.VoiceCall, bool, error) {
	call, err := scanCall(r.db.QueryRowContext(ctx, `SELECT `+callColumns+` FROM platform_voice_calls WHERE id = $1`, strings.TrimSpace(id)))
	if err == sql.ErrNoRows {
		return domain.VoiceCall{}, false, nil
	}
	return call, err == nil, err
}

const callFilterSQL = `
WHERE ($1::text = '' OR direction = $1::text)
  AND ($2::text = '' OR status = $2::text)
  AND ($3::text = '' OR position(lower($3::text) in lower(contact_name || ' ' || to_number || ' ' || from_number)) > 0)
  AND (NOT $4::boolean OR recording_url <> '')`

func (r *PostgresVoiceRepository) ListCalls(ctx context.Context, filter domain.VoiceCallFilter) ([]domain.VoiceCall, int, error) {
	var total int
	args := []any{filter.Direction, filter.Status, strings.TrimSpace(filter.Query), filter.HasRecording}
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM platform_voice_calls`+callFilterSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+callColumns+` FROM platform_voice_calls`+callFilterSQL+` ORDER BY started_at DESC, id DESC LIMIT $5 OFFSET $6`,
		append(args, filter.Limit, (filter.Page-1)*filter.Limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	calls := []domain.VoiceCall{}
	for rows.Next() {
		call, err := scanCall(rows)
		if err != nil {
			return nil, 0, err
		}
		calls = append(calls, call)
	}
	return calls, total, rows.Err()
}

func (r *PostgresVoiceRepository) CallsSince(ctx context.Context, since time.Time) ([]domain.VoiceCall, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+callColumns+` FROM platform_voice_calls WHERE started_at >= $1 ORDER BY started_at ASC`, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	calls := []domain.VoiceCall{}
	for rows.Next() {
		call, err := scanCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, rows.Err()
}

type InMemoryVoiceRepository struct {
	mu      sync.RWMutex
	prompts map[string]domain.IVRPrompt
	calls   map[string]domain.VoiceCall
}

func NewInMemoryVoiceRepository() *InMemoryVoiceRepository {
	return &InMemoryVoiceRepository{prompts: map[string]domain.IVRPrompt{}, calls: map[string]domain.VoiceCall{}}
}

func (r *InMemoryVoiceRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemoryVoiceRepository) ListPrompts(context.Context) ([]domain.IVRPrompt, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prompts := make([]domain.IVRPrompt, 0, len(r.prompts))
	for _, prompt := range r.prompts {
		prompts = append(prompts, prompt)
	}
	sort.Slice(prompts, func(i, j int) bool {
		if prompts[i].Category != prompts[j].Category {
			return prompts[i].Category < prompts[j].Category
		}
		return strings.ToLower(prompts[i].Name) < strings.ToLower(prompts[j].Name)
	})
	return prompts, nil
}

func (r *InMemoryVoiceRepository) FindPrompt(_ context.Context, id string) (domain.IVRPrompt, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prompt, ok := r.prompts[strings.TrimSpace(id)]
	return prompt, ok, nil
}

func (r *InMemoryVoiceRepository) SavePrompt(_ context.Context, prompt domain.IVRPrompt) (domain.IVRPrompt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompts[prompt.ID] = prompt
	return prompt, nil
}

func (r *InMemoryVoiceRepository) DeletePrompt(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.prompts[strings.TrimSpace(id)]
	delete(r.prompts, strings.TrimSpace(id))
	return ok, nil
}

func (r *InMemoryVoiceRepository) SaveCall(_ context.Context, call domain.VoiceCall) (domain.VoiceCall, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[call.ID] = call
	return call, nil
}

func (r *InMemoryVoiceRepository) FindCall(_ context.Context, id string) (domain.VoiceCall, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	call, ok := r.calls[strings.TrimSpace(id)]
	return call, ok, nil
}

func (r *InMemoryVoiceRepository) sortedCalls(match func(domain.VoiceCall) bool) []domain.VoiceCall {
	calls := []domain.VoiceCall{}
	for _, call := range r.calls {
		if match(call) {
			calls = append(calls, call)
		}
	}
	sort.Slice(calls, func(i, j int) bool {
		if !calls[i].StartedAt.Equal(calls[j].StartedAt) {
			return calls[i].StartedAt.After(calls[j].StartedAt)
		}
		return calls[i].ID > calls[j].ID
	})
	return calls
}

func (r *InMemoryVoiceRepository) ListCalls(_ context.Context, filter domain.VoiceCallFilter) ([]domain.VoiceCall, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	needle := strings.ToLower(strings.TrimSpace(filter.Query))
	all := r.sortedCalls(func(call domain.VoiceCall) bool {
		haystack := strings.ToLower(call.ContactName + " " + call.ToNumber + " " + call.FromNumber)
		return (filter.Direction == "" || call.Direction == filter.Direction) &&
			(filter.Status == "" || call.Status == filter.Status) &&
			strings.Contains(haystack, needle) &&
			(!filter.HasRecording || call.RecordingURL != "")
	})
	start := min((filter.Page-1)*filter.Limit, len(all))
	end := min(start+filter.Limit, len(all))
	return all[start:end], len(all), nil
}

func (r *InMemoryVoiceRepository) CallsSince(_ context.Context, since time.Time) ([]domain.VoiceCall, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedCalls(func(call domain.VoiceCall) bool { return !call.StartedAt.Before(since) }), nil
}
