package domain

import (
	"sort"
	"strings"
)

// ConversationPatch changes the staff-owned fields of a conversation. A nil
// field is left alone.
type ConversationPatch struct {
	Name           *string
	Status         *string
	Priority       *string
	AssigneeUserID *string
	Tags           *[]string
	MarkedUnread   *bool
}

func (p ConversationPatch) Empty() bool {
	return p.Name == nil && p.Status == nil && p.Priority == nil && p.AssigneeUserID == nil && p.Tags == nil && p.MarkedUnread == nil
}

func NormalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		value := strings.ToLower(strings.TrimSpace(tag))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func ApplyConversationPatch(c WhatsAppConversation, p ConversationPatch) WhatsAppConversation {
	if p.Name != nil {
		c.DriverName = strings.TrimSpace(*p.Name)
	}
	if p.Status != nil {
		c.Status = *p.Status
	}
	if p.Priority != nil {
		c.Priority = *p.Priority
	}
	if p.AssigneeUserID != nil {
		c.AssigneeUserID = strings.TrimSpace(*p.AssigneeUserID)
	}
	if p.Tags != nil {
		c.Tags = append([]string(nil), *p.Tags...)
	}
	if p.MarkedUnread != nil {
		c.MarkedUnread = *p.MarkedUnread
	}
	return c
}

// EffectiveUnread counts a conversation staff marked unread as one unread
// message even when every inbound message was read.
func EffectiveUnread(count int, markedUnread bool) int {
	if markedUnread && count == 0 {
		return 1
	}
	return count
}
