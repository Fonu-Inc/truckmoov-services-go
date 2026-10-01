package domain

import (
	"sort"
	"strings"
	"time"
)

const (
	InboxStateAll    = "all"
	InboxStateUnread = "unread"
)

type ConversationCursor struct {
	SortAt time.Time
	ID     string
}

func ConversationSortAt(c WhatsAppConversation) time.Time {
	if c.LastMessageAt != nil {
		return c.LastMessageAt.UTC()
	}
	return c.CreatedAt.UTC()
}

func ConversationMatches(c WhatsAppConversation, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return true
	}
	for _, field := range []string{c.DriverPhone, c.DriverName, c.LastMessagePreview} {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

// PageConversations is the reference ordering: newest activity first, id as
// the tiebreak, keyset after the cursor. Returns at most limit items.
func PageConversations(all []WhatsAppConversation, query, state string, cursor *ConversationCursor, limit int) []WhatsAppConversation {
	matched := make([]WhatsAppConversation, 0, len(all))
	for _, c := range all {
		if !ConversationMatches(c, query) || (state == InboxStateUnread && c.UnreadCount == 0) {
			continue
		}
		if cursor != nil && !conversationBefore(c, *cursor) {
			continue
		}
		matched = append(matched, c)
	}
	sort.Slice(matched, func(i, j int) bool {
		left, right := ConversationSortAt(matched[i]), ConversationSortAt(matched[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return matched[i].ID > matched[j].ID
	})
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched
}

func conversationBefore(c WhatsAppConversation, cursor ConversationCursor) bool {
	at := ConversationSortAt(c)
	if !at.Equal(cursor.SortAt) {
		return at.Before(cursor.SortAt)
	}
	return c.ID < cursor.ID
}

// PageMessages returns up to limit messages older than the message named by
// before (all when empty), newest first. An unknown before yields nothing.
func PageMessages(all []WhatsAppConversationMessage, before string, limit int) []WhatsAppConversationMessage {
	sorted := append([]WhatsAppConversationMessage(nil), all...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].ID > sorted[j].ID
	})
	start := 0
	if before != "" {
		start = len(sorted)
		for index, message := range sorted {
			if message.ID == before {
				start = index + 1
				break
			}
		}
	}
	sorted = sorted[start:]
	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

func UnreadInbound(messages []WhatsAppConversationMessage, readAt *time.Time) int {
	count := 0
	for _, message := range messages {
		if message.Direction != WhatsAppDirectionInbound {
			continue
		}
		if readAt == nil || message.CreatedAt.After(*readAt) {
			count++
		}
	}
	return count
}
