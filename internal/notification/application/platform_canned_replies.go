package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

var (
	ErrCannedReplyShortcutTaken = errors.New("that shortcut is already in use")
	ErrCannedReplyNotFound      = errors.New("canned reply not found")

	cannedShortcutPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
)

const maxCannedTitleLength = 80

type CannedReplyInput struct {
	ID       string
	Shortcut string
	Title    string
	Body     string
	ActorID  string
}

// CannedReplies are the saved answers staff insert with a slash shortcut.
type CannedReplies struct {
	repository CannedReplyRepository
	clock      Clock
	ids        IDGenerator
}

func NewCannedReplies(repository CannedReplyRepository, clock Clock, ids IDGenerator) *CannedReplies {
	return &CannedReplies{repository: repository, clock: clock, ids: ids}
}

func (u *CannedReplies) List(ctx context.Context) ([]domain.CannedReply, error) {
	return u.repository.ListCannedReplies(ctx)
}

func (u *CannedReplies) Save(ctx context.Context, input CannedReplyInput) (domain.CannedReply, error) {
	reply, err := validCannedReply(input)
	if err != nil {
		return domain.CannedReply{}, err
	}
	now := u.clock.Now().UTC()
	reply.UpdatedAt = now
	reply.CreatedAt = now
	reply.CreatedBy = input.ActorID
	reply.ID = strings.TrimSpace(input.ID)
	if reply.ID == "" {
		reply.ID = u.ids.New("cr")
		return u.repository.SaveCannedReply(ctx, reply)
	}
	if !u.exists(ctx, reply.ID) {
		return domain.CannedReply{}, ErrCannedReplyNotFound
	}
	return u.repository.SaveCannedReply(ctx, reply)
}

func (u *CannedReplies) Delete(ctx context.Context, id string) error {
	deleted, err := u.repository.DeleteCannedReply(ctx, id)
	if err == nil && !deleted {
		err = ErrCannedReplyNotFound
	}
	return err
}

func (u *CannedReplies) exists(ctx context.Context, id string) bool {
	replies, err := u.repository.ListCannedReplies(ctx)
	if err != nil {
		return false
	}
	for _, reply := range replies {
		if reply.ID == id {
			return true
		}
	}
	return false
}

func validCannedReply(input CannedReplyInput) (domain.CannedReply, error) {
	shortcut := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input.Shortcut), "/")))
	title := strings.TrimSpace(input.Title)
	body := strings.TrimSpace(input.Body)
	switch {
	case !cannedShortcutPattern.MatchString(shortcut):
		return domain.CannedReply{}, InboxInputError("shortcut must be 1 to 32 letters, numbers, dashes or underscores")
	case title == "" || utf8.RuneCountInString(title) > maxCannedTitleLength:
		return domain.CannedReply{}, InboxInputError("title must be 1 to 80 characters")
	case body == "" || utf8.RuneCountInString(body) > maxPlatformMessageText || hasDisallowedControl(body):
		return domain.CannedReply{}, InboxInputError("reply must be 1 to 4096 characters")
	}
	return domain.CannedReply{Shortcut: shortcut, Title: title, Body: body}, nil
}
