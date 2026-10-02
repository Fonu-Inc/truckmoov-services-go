package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/identity"
)

const (
	maxContactNameLength = 120
	maxBulkConversations = 100
)

// InboxInputError is a request the staff can fix, so it is shown as is.
type InboxInputError string

func (e InboxInputError) Error() string { return string(e) }

type ConversationUpdate struct {
	Name           *string
	Status         *string
	Priority       *string
	AssigneeUserID *string
	Tags           *[]string
}

type BulkOperation struct {
	Mark   string
	Update ConversationUpdate
}

const (
	BulkMarkRead   = "read"
	BulkMarkUnread = "unread"
)

// PlatformInboxAdmin changes the staff-owned side of a conversation: who has
// it, where it stands, how it is tagged, and whether it counts as unread.
type PlatformInboxAdmin struct {
	repository PlatformInboxAdminRepository
	notifier   *PlatformMessagingNotifier
	clock      Clock
	ids        IDGenerator
}

func NewPlatformInboxAdmin(repository PlatformInboxAdminRepository, notifier *PlatformMessagingNotifier, clock Clock, ids IDGenerator) *PlatformInboxAdmin {
	return &PlatformInboxAdmin{repository: repository, notifier: notifier, clock: clock, ids: ids}
}

func (u *PlatformInboxAdmin) Update(ctx context.Context, conversationID string, update ConversationUpdate) (ConversationView, error) {
	patch, err := update.toPatch()
	if err != nil {
		return ConversationView{}, err
	}
	conversation, err := u.patch(ctx, conversationID, patch)
	if err != nil {
		return ConversationView{}, err
	}
	return u.view(ctx, conversation)
}

func (u *PlatformInboxAdmin) Bulk(ctx context.Context, ids []string, operation BulkOperation) (int, error) {
	ids = uniqueIDs(ids)
	if len(ids) == 0 || len(ids) > maxBulkConversations {
		return 0, InboxInputError("choose between 1 and 100 conversations")
	}
	patch, err := operation.toPatch()
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, id := range ids {
		if err := u.applyBulk(ctx, id, operation.Mark, patch); err == nil {
			changed++
		} else if err != ErrWhatsAppConversationNotFound {
			return changed, err
		}
	}
	return changed, nil
}

func (u *PlatformInboxAdmin) applyBulk(ctx context.Context, id, mark string, patch domain.ConversationPatch) error {
	if mark == BulkMarkRead {
		return u.markRead(ctx, id)
	}
	_, err := u.patch(ctx, id, patch)
	return err
}

func (u *PlatformInboxAdmin) markRead(ctx context.Context, id string) error {
	return NewPlatformMessagingReadUseCase(u.repository, u.notifier, u.clock).Execute(ctx, id)
}

func (u *PlatformInboxAdmin) patch(ctx context.Context, id string, patch domain.ConversationPatch) (domain.WhatsAppConversation, error) {
	conversation, ok, err := u.repository.PatchConversation(ctx, identity.SystemTenantID, strings.TrimSpace(id), patch, u.clock.Now())
	if err != nil {
		return domain.WhatsAppConversation{}, err
	}
	if !ok {
		return domain.WhatsAppConversation{}, ErrWhatsAppConversationNotFound
	}
	u.notifier.ConversationUpdated(ctx, conversation)
	return conversation, nil
}

func (u *PlatformInboxAdmin) view(ctx context.Context, conversation domain.WhatsAppConversation) (ConversationView, error) {
	unread, err := u.repository.ConversationUnreadCount(ctx, conversation.TenantID, conversation.ID)
	if err != nil {
		return ConversationView{}, err
	}
	conversation.UnreadCount = unread
	return NewConversationView(conversation, u.clock.Now()), nil
}

// AddNote stores a private staff note in the thread. It is never sent to the
// customer and does not change the conversation's last message.
func (u *PlatformInboxAdmin) AddNote(ctx context.Context, conversationID, actorID, text string) (MessageView, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxPlatformMessageText || hasDisallowedControl(text) {
		return MessageView{}, InboxInputError("a note must be 1 to 4096 characters")
	}
	conversation, ok, err := u.repository.FindConversationByTenantAndID(ctx, identity.SystemTenantID, strings.TrimSpace(conversationID))
	if err != nil {
		return MessageView{}, err
	}
	if !ok {
		return MessageView{}, ErrWhatsAppConversationNotFound
	}
	now := u.clock.Now().UTC()
	saved, err := u.repository.SaveMessage(ctx, domain.WhatsAppConversationMessage{
		ID:             u.ids.New("wam"),
		TenantID:       conversation.TenantID,
		ConversationID: conversation.ID,
		Direction:      domain.WhatsAppDirectionInternal,
		MessageType:    domain.WhatsAppMessageTypeNote,
		Status:         "sent",
		SenderRole:     platformStaffRole,
		SenderSubject:  actorID,
		Text:           text,
		Payload:        map[string]any{"type": domain.WhatsAppMessageTypeNote},
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		return MessageView{}, err
	}
	u.notifier.MessageCreated(ctx, saved)
	return NewMessageView(saved), nil
}

func (o BulkOperation) toPatch() (domain.ConversationPatch, error) {
	switch o.Mark {
	case BulkMarkRead:
		return domain.ConversationPatch{}, nil
	case BulkMarkUnread:
		marked := true
		return domain.ConversationPatch{MarkedUnread: &marked}, nil
	case "":
		patch, err := o.Update.toPatch()
		if err == nil && patch.Empty() {
			err = InboxInputError("nothing to change")
		}
		return patch, err
	default:
		return domain.ConversationPatch{}, InboxInputError("unknown bulk action")
	}
}

func (u ConversationUpdate) toPatch() (domain.ConversationPatch, error) {
	patch := domain.ConversationPatch{}
	if u.Name != nil {
		name := strings.TrimSpace(*u.Name)
		if utf8.RuneCountInString(name) > maxContactNameLength || hasDisallowedControl(name) {
			return patch, InboxInputError("name must be at most 120 characters")
		}
		patch.Name = &name
	}
	if u.Status != nil {
		if !domain.IsConversationStatus(*u.Status) {
			return patch, InboxInputError("status must be open, pending or resolved")
		}
		patch.Status = u.Status
	}
	if u.Priority != nil {
		if !domain.IsConversationPriority(*u.Priority) {
			return patch, InboxInputError("priority must be low, normal, high or urgent")
		}
		patch.Priority = u.Priority
	}
	if u.AssigneeUserID != nil {
		assignee := strings.TrimSpace(*u.AssigneeUserID)
		if len(assignee) > maxPlatformIDLength {
			return patch, InboxInputError("assignee is not valid")
		}
		patch.AssigneeUserID = &assignee
	}
	if u.Tags != nil {
		tags, err := validTags(*u.Tags)
		if err != nil {
			return patch, err
		}
		patch.Tags = &tags
	}
	return patch, nil
}

func validTags(raw []string) ([]string, error) {
	tags := domain.NormalizeTags(raw)
	if len(tags) > domain.MaxConversationTags {
		return nil, InboxInputError("at most 10 tags per conversation")
	}
	for _, tag := range tags {
		if utf8.RuneCountInString(tag) > domain.MaxConversationTagLength || hasDisallowedControl(tag) {
			return nil, InboxInputError("a tag must be at most 32 characters")
		}
	}
	return tags, nil
}

func uniqueIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if validID(id) && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique
}
