package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

var (
	_ application.SMSTemplateRepository = (*PostgresSMSTemplateRepository)(nil)
	_ application.SMSTemplateRepository = (*InMemorySMSTemplateRepository)(nil)
)

const smsTemplateSchema = `
CREATE TABLE IF NOT EXISTS platform_sms_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'general',
    content TEXT NOT NULL,
    variables JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_platform_sms_templates_name ON platform_sms_templates (lower(name));
`

const smsTemplateColumns = `id, name, category, content, variables, is_active, created_by, created_at, updated_at`

type PostgresSMSTemplateRepository struct{ db *sql.DB }

func NewPostgresSMSTemplateRepository(db *sql.DB) *PostgresSMSTemplateRepository {
	return &PostgresSMSTemplateRepository{db: db}
}

func (r *PostgresSMSTemplateRepository) EnsureSchema(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, smsTemplateSchema); err != nil {
		return fmt.Errorf("ensure sms template schema: %w", err)
	}
	return nil
}

func scanSMSTemplate(scanner sqlScanner) (domain.SMSTemplate, error) {
	var template domain.SMSTemplate
	var variables []byte
	if err := scanner.Scan(&template.ID, &template.Name, &template.Category, &template.Content, &variables, &template.IsActive, &template.CreatedBy, &template.CreatedAt, &template.UpdatedAt); err != nil {
		return domain.SMSTemplate{}, err
	}
	_ = json.Unmarshal(variables, &template.Variables)
	if template.Variables == nil {
		template.Variables = []string{}
	}
	template.CreatedAt, template.UpdatedAt = template.CreatedAt.UTC(), template.UpdatedAt.UTC()
	return template, nil
}

func (r *PostgresSMSTemplateRepository) ListSMSTemplates(ctx context.Context) ([]domain.SMSTemplate, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+smsTemplateColumns+` FROM platform_sms_templates ORDER BY lower(name) ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	templates := []domain.SMSTemplate{}
	for rows.Next() {
		template, err := scanSMSTemplate(rows)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, rows.Err()
}

func (r *PostgresSMSTemplateRepository) FindSMSTemplate(ctx context.Context, id string) (domain.SMSTemplate, bool, error) {
	template, err := scanSMSTemplate(r.db.QueryRowContext(ctx, `SELECT `+smsTemplateColumns+` FROM platform_sms_templates WHERE id = $1`, strings.TrimSpace(id)))
	if err == sql.ErrNoRows {
		return domain.SMSTemplate{}, false, nil
	}
	return template, err == nil, err
}

func (r *PostgresSMSTemplateRepository) SaveSMSTemplate(ctx context.Context, template domain.SMSTemplate) (domain.SMSTemplate, error) {
	variables, err := json.Marshal(template.Variables)
	if err != nil {
		return domain.SMSTemplate{}, err
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO platform_sms_templates (`+smsTemplateColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, category = EXCLUDED.category, content = EXCLUDED.content,
    variables = EXCLUDED.variables, is_active = EXCLUDED.is_active, updated_at = EXCLUDED.updated_at
`, template.ID, template.Name, template.Category, template.Content, variables, template.IsActive, template.CreatedBy, template.CreatedAt.UTC(), template.UpdatedAt.UTC())
	if isUniqueViolation(err) {
		return domain.SMSTemplate{}, application.ErrSMSTemplateNameTaken
	}
	return template, err
}

func (r *PostgresSMSTemplateRepository) DeleteSMSTemplate(ctx context.Context, id string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM platform_sms_templates WHERE id = $1`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	deleted, err := result.RowsAffected()
	return deleted > 0, err
}

type InMemorySMSTemplateRepository struct {
	mu        sync.RWMutex
	templates map[string]domain.SMSTemplate
}

func NewInMemorySMSTemplateRepository() *InMemorySMSTemplateRepository {
	return &InMemorySMSTemplateRepository{templates: map[string]domain.SMSTemplate{}}
}

func (r *InMemorySMSTemplateRepository) EnsureSchema(context.Context) error { return nil }

func (r *InMemorySMSTemplateRepository) ListSMSTemplates(context.Context) ([]domain.SMSTemplate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	templates := make([]domain.SMSTemplate, 0, len(r.templates))
	for _, template := range r.templates {
		templates = append(templates, template)
	}
	sort.Slice(templates, func(i, j int) bool { return strings.ToLower(templates[i].Name) < strings.ToLower(templates[j].Name) })
	return templates, nil
}

func (r *InMemorySMSTemplateRepository) FindSMSTemplate(_ context.Context, id string) (domain.SMSTemplate, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	template, ok := r.templates[strings.TrimSpace(id)]
	return template, ok, nil
}

func (r *InMemorySMSTemplateRepository) SaveSMSTemplate(_ context.Context, template domain.SMSTemplate) (domain.SMSTemplate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.templates {
		if existing.ID != template.ID && strings.EqualFold(existing.Name, template.Name) {
			return domain.SMSTemplate{}, application.ErrSMSTemplateNameTaken
		}
	}
	r.templates[template.ID] = template
	return template, nil
}

func (r *InMemorySMSTemplateRepository) DeleteSMSTemplate(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.templates[strings.TrimSpace(id)]
	delete(r.templates, strings.TrimSpace(id))
	return ok, nil
}
