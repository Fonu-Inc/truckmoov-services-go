package domain

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

var smsPlaceholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_]*)\s*\}\}`)

type SMSTemplate struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Content   string    `json:"content"`
	Variables []string  `json:"variables"`
	IsActive  bool      `json:"is_active"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var smsTemplateCategories = map[string]bool{"general": true, "job": true, "delivery": true, "safety": true, "payment": true}

func IsSMSTemplateCategory(value string) bool { return smsTemplateCategories[value] }

// SMSTemplateVariables lists the placeholders in the order they first appear.
func SMSTemplateVariables(content string) []string {
	seen := map[string]bool{}
	variables := []string{}
	for _, match := range smsPlaceholderPattern.FindAllStringSubmatch(content, -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			variables = append(variables, match[1])
		}
	}
	return variables
}

// RenderSMSTemplate fills the placeholders. A placeholder with no value is
// reported rather than sent as raw braces.
func RenderSMSTemplate(content string, values map[string]string) (string, []string) {
	missing := map[string]bool{}
	rendered := smsPlaceholderPattern.ReplaceAllStringFunc(content, func(match string) string {
		name := smsPlaceholderPattern.FindStringSubmatch(match)[1]
		if value := strings.TrimSpace(values[name]); value != "" {
			return value
		}
		missing[name] = true
		return match
	})
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	return rendered, names
}
