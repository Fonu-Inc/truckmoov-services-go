package application

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

const (
	defaultConversationPage = 30
	defaultMessagePage      = 50
	maxInboxPage            = 100
)

var ErrInvalidInboxCursor = errors.New("cursor is not valid")

type ListInboxQuery struct {
	Query    string
	State    string
	Status   string
	Assignee string
	Tag      string
	Cursor   string
	Limit    int
}

type ConversationPage struct {
	Items      []ConversationView `json:"items"`
	NextCursor *string            `json:"next_cursor"`
}

type MessagePage struct {
	Items      []MessageView `json:"items"`
	NextBefore *string       `json:"next_before"`
}

// PlatformInboxQueries reads the platform conversations for the staff inbox.
type PlatformInboxQueries struct {
	repository PlatformMessagingRepository
	clock      Clock
}

func NewPlatformInboxQueries(repository PlatformMessagingRepository, clock Clock) *PlatformInboxQueries {
	return &PlatformInboxQueries{repository: repository, clock: clock}
}

func (q *PlatformInboxQueries) ListConversations(ctx context.Context, query ListInboxQuery) (ConversationPage, error) {
	cursor, err := decodeInboxCursor(query.Cursor)
	if err != nil {
		return ConversationPage{}, err
	}
	limit := clampLimit(query.Limit, defaultConversationPage)
	rows, err := q.repository.ListConversationsFiltered(ctx, identity.SystemTenantID, inboxFilterOf(query), cursor, limit+1)
	if err != nil {
		return ConversationPage{}, err
	}
	page := ConversationPage{Items: make([]ConversationView, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		next := encodeInboxCursor(rows[limit-1])
		page.NextCursor = &next
	}
	now := q.clock.Now()
	for _, row := range rows {
		page.Items = append(page.Items, NewConversationView(row, now))
	}
	return page, nil
}

type UnreadSummary struct {
	Conversations int  `json:"conversations"`
	AtLeast       bool `json:"at_least"`
}

// UnreadSummary counts conversations with unread messages, up to one page; it
// feeds the badge on the navigation item.
func (q *PlatformInboxQueries) UnreadSummary(ctx context.Context) (UnreadSummary, error) {
	filter := domain.InboxFilter{State: domain.InboxStateUnread}
	rows, err := q.repository.ListConversationsFiltered(ctx, identity.SystemTenantID, filter, nil, maxInboxPage+1)
	if err != nil {
		return UnreadSummary{}, err
	}
	if len(rows) > maxInboxPage {
		return UnreadSummary{Conversations: maxInboxPage, AtLeast: true}, nil
	}
	return UnreadSummary{Conversations: len(rows)}, nil
}

func (q *PlatformInboxQueries) GetConversation(ctx context.Context, id string) (ConversationView, error) {
	conversation, ok, err := q.repository.FindConversationByTenantAndID(ctx, identity.SystemTenantID, strings.TrimSpace(id))
	if err != nil {
		return ConversationView{}, err
	}
	if !ok {
		return ConversationView{}, ErrWhatsAppConversationNotFound
	}
	conversation.UnreadCount, err = q.repository.ConversationUnreadCount(ctx, conversation.TenantID, conversation.ID)
	if err != nil {
		return ConversationView{}, err
	}
	return NewConversationView(conversation, q.clock.Now()), nil
}

func (q *PlatformInboxQueries) ListMessages(ctx context.Context, conversationID, before string, limit int) (MessagePage, error) {
	if _, ok, err := q.repository.FindConversationByTenantAndID(ctx, identity.SystemTenantID, strings.TrimSpace(conversationID)); err != nil {
		return MessagePage{}, err
	} else if !ok {
		return MessagePage{}, ErrWhatsAppConversationNotFound
	}
	size := clampLimit(limit, defaultMessagePage)
	rows, err := q.repository.ListMessagesPage(ctx, identity.SystemTenantID, strings.TrimSpace(conversationID), strings.TrimSpace(before), size+1)
	if err != nil {
		return MessagePage{}, err
	}
	page := MessagePage{Items: make([]MessageView, 0, len(rows))}
	if len(rows) > size {
		rows = rows[:size]
		oldest := rows[size-1].ID
		page.NextBefore = &oldest
	}
	for index := len(rows) - 1; index >= 0; index-- {
		page.Items = append(page.Items, NewMessageView(rows[index]))
	}
	return page, nil
}

func inboxFilterOf(query ListInboxQuery) domain.InboxFilter {
	filter := domain.InboxFilter{
		Query:    strings.TrimSpace(query.Query),
		State:    domain.InboxStateAll,
		Assignee: strings.TrimSpace(query.Assignee),
		Tag:      strings.ToLower(strings.TrimSpace(query.Tag)),
	}
	if strings.EqualFold(strings.TrimSpace(query.State), domain.InboxStateUnread) {
		filter.State = domain.InboxStateUnread
	}
	if status := strings.ToLower(strings.TrimSpace(query.Status)); domain.IsConversationStatus(status) {
		filter.Status = status
	}
	return filter
}

func clampLimit(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > maxInboxPage {
		return maxInboxPage
	}
	return limit
}

func encodeInboxCursor(c domain.WhatsAppConversation) string {
	raw := strconv.FormatInt(domain.ConversationSortAt(c).UnixNano(), 10) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeInboxCursor(value string) (*domain.ConversationCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, ErrInvalidInboxCursor
	}
	nanos, id, found := strings.Cut(string(raw), "|")
	parsed, parseErr := strconv.ParseInt(nanos, 10, 64)
	if !found || parseErr != nil || id == "" {
		return nil, ErrInvalidInboxCursor
	}
	return &domain.ConversationCursor{SortAt: time.Unix(0, parsed).UTC(), ID: id}, nil
}
