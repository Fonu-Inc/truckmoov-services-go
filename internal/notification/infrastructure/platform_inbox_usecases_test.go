package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type countingIDs struct {
	mu   sync.Mutex
	next int
}

func (c *countingIDs) New(prefix string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	return fmt.Sprintf("%s-%d", prefix, c.next)
}

type noDeliveryEvents struct{}

func (noDeliveryEvents) PublishDeliveryUpdated(context.Context, domain.MessageDeliveryUpdated) error {
	return nil
}

type inboxEvents struct {
	mu    sync.Mutex
	names []string
	views []application.ConversationView
}

func (e *inboxEvents) record(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.names = append(e.names, name)
}

func (e *inboxEvents) PublishMessageCreated(context.Context, application.MessageCreatedView) error {
	e.record("message.created")
	return nil
}

func (e *inboxEvents) PublishMessageStatus(context.Context, application.MessageStatusView) error {
	e.record("message.status")
	return nil
}

func (e *inboxEvents) PublishConversationUpdated(_ context.Context, view application.ConversationUpdatedView) error {
	e.record("conversation.updated")
	e.views = append(e.views, view.Conversation)
	return nil
}

func (e *inboxEvents) PublishMessageDeleted(context.Context, application.MessageDeletedView) error {
	e.record("message.deleted")
	return nil
}

func (e *inboxEvents) count(name string) int {
	n := 0
	for _, got := range e.names {
		if got == name {
			n++
		}
	}
	return n
}

type recordingProvider struct {
	texts   []string
	media   []domain.WhatsAppMediaMessageRequest
	uploads int
	fail    error
}

func (p *recordingProvider) SendText(_ context.Context, _, _, content string) (domain.WhatsAppSendResult, error) {
	if p.fail != nil {
		return domain.WhatsAppSendResult{}, p.fail
	}
	p.texts = append(p.texts, content)
	return domain.WhatsAppSendResult{Status: "accepted", MessageID: fmt.Sprintf("wamid.t%d", len(p.texts))}, nil
}

func (p *recordingProvider) SendMedia(_ context.Context, request domain.WhatsAppMediaMessageRequest) (domain.WhatsAppSendResult, error) {
	p.media = append(p.media, request)
	return domain.WhatsAppSendResult{Status: "accepted", MessageID: fmt.Sprintf("wamid.m%d", len(p.media))}, nil
}

func (p *recordingProvider) UploadMedia(context.Context, string, string, string, []byte) (string, error) {
	p.uploads++
	return fmt.Sprintf("media-%d", p.uploads), nil
}

func (p *recordingProvider) DownloadMedia(context.Context, string, string) ([]byte, string, error) {
	return []byte("bytes"), "image/png", nil
}

type inboxKit struct {
	repository *InMemoryWhatsAppConversationRepository
	events     *inboxEvents
	provider   *recordingProvider
	clock      *testClock
	admin      *application.PlatformInboxAdmin
	send       *application.PlatformMessagingSendUseCase
	media      *application.PlatformMediaSendUseCase
	retry      *application.PlatformRetryUseCase
	scheduled  *application.PlatformScheduledSends
}

func newInboxKit(t *testing.T) *inboxKit {
	t.Helper()
	repository := NewInMemoryWhatsAppConversationRepository()
	events := &inboxEvents{}
	provider := &recordingProvider{}
	clock := &testClock{now: inboxEpoch}
	ids := &countingIDs{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := application.NewPlatformMessagingNotifier(repository, events, clock, log)
	text := application.NewSendWhatsAppTextMessageUseCase(NewInMemoryOutboxRepository(), repository, provider, noDeliveryEvents{}, clock, ids, inboxTenant)
	send := application.NewPlatformMessagingSendUseCase(repository, text, notifier, clock, ids)
	media := application.NewPlatformMediaSendUseCase(repository, provider, notifier, clock, ids)
	return &inboxKit{
		repository: repository, events: events, provider: provider, clock: clock,
		admin:     application.NewPlatformInboxAdmin(repository, notifier, clock, ids),
		send:      send,
		media:     media,
		retry:     application.NewPlatformRetryUseCase(repository, send, media, nil, notifier),
		scheduled: application.NewPlatformScheduledSends(send, repository),
	}
}

func (k *inboxKit) openConversation(t *testing.T) {
	t.Helper()
	opens := inboxEpoch.Add(24 * time.Hour)
	if _, err := k.repository.SaveConversation(context.Background(), domain.WhatsAppConversation{
		ID: "wac-1", TenantID: inboxTenant, DriverPhone: "2348000000001", DriverName: "Ada",
		LastInboundMessageAt: &inboxEpoch, WindowExpiresAt: &opens, LastMessageAt: &inboxEpoch,
		LastMessagePreview: "hello", LastMessageDirection: "inbound", CreatedAt: inboxEpoch, UpdatedAt: inboxEpoch,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAdminUpdateValidatesAndAnnounces(t *testing.T) {
	kit := newInboxKit(t)
	kit.openConversation(t)
	ctx := context.Background()

	view, err := kit.admin.Update(ctx, "wac-1", application.ConversationUpdate{
		Status: ptr("pending"), Priority: ptr("high"), AssigneeUserID: ptr("u-1"), Tags: &[]string{" VIP ", "vip", "Billing"},
	})
	if err != nil || view.Status != "pending" || view.Priority != "high" || view.AssigneeUserID == nil || *view.AssigneeUserID != "u-1" || fmt.Sprint(view.Tags) != "[billing vip]" {
		t.Fatalf("view: %+v err=%v", view, err)
	}
	if kit.events.count("conversation.updated") != 1 {
		t.Fatalf("events: %v", kit.events.names)
	}

	var input application.InboxInputError
	for label, update := range map[string]application.ConversationUpdate{
		"status":   {Status: ptr("closed")},
		"priority": {Priority: ptr("asap")},
		"tags":     {Tags: &[]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}},
	} {
		if _, err := kit.admin.Update(ctx, "wac-1", update); !errors.As(err, &input) {
			t.Errorf("%s must be refused as input error, got %v", label, err)
		}
	}
	if _, err := kit.admin.Update(ctx, "missing", application.ConversationUpdate{Status: ptr("open")}); !errors.Is(err, application.ErrWhatsAppConversationNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestNotesStayPrivateAndLeaveTheSummaryAlone(t *testing.T) {
	kit := newInboxKit(t)
	kit.openConversation(t)
	ctx := context.Background()

	note, err := kit.admin.AddNote(ctx, "wac-1", "u-1", "  call the depot  ")
	if err != nil || note.MessageType != "note" || note.Direction != "internal" || note.Text == nil || *note.Text != "call the depot" || note.AuthorID == nil || *note.AuthorID != "u-1" {
		t.Fatalf("note: %+v err=%v", note, err)
	}
	conversation, _, _ := kit.repository.FindConversationByTenantAndID(ctx, inboxTenant, "wac-1")
	if conversation.LastMessagePreview != "hello" || conversation.LastMessageDirection != "inbound" {
		t.Fatalf("a note must not change the preview: %+v", conversation)
	}
	if len(kit.provider.texts) != 0 || kit.events.count("message.created") != 1 {
		t.Fatalf("a note is never sent: texts=%v events=%v", kit.provider.texts, kit.events.names)
	}
	var input application.InboxInputError
	if _, err := kit.admin.AddNote(ctx, "wac-1", "u-1", "   "); !errors.As(err, &input) {
		t.Fatalf("blank note: %v", err)
	}
}

func TestBulkReadUnreadAndResolve(t *testing.T) {
	kit := newInboxKit(t)
	kit.openConversation(t)
	ctx := context.Background()

	if changed, err := kit.admin.Bulk(ctx, []string{"wac-1", "wac-1", "missing"}, application.BulkOperation{Mark: application.BulkMarkUnread}); err != nil || changed != 1 {
		t.Fatalf("unread: %d %v", changed, err)
	}
	if count, _ := kit.repository.ConversationUnreadCount(ctx, inboxTenant, "wac-1"); count == 0 {
		t.Fatal("marked unread must count")
	}
	if _, err := kit.admin.Bulk(ctx, []string{"wac-1"}, application.BulkOperation{Mark: application.BulkMarkRead}); err != nil {
		t.Fatal(err)
	}
	if count, _ := kit.repository.ConversationUnreadCount(ctx, inboxTenant, "wac-1"); count != 0 {
		t.Fatalf("read = %d", count)
	}
	if _, err := kit.admin.Bulk(ctx, []string{"wac-1"}, application.BulkOperation{Update: application.ConversationUpdate{Status: ptr("resolved")}}); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := kit.repository.FindConversationByTenantAndID(ctx, inboxTenant, "wac-1"); c.Status != "resolved" {
		t.Fatalf("status = %s", c.Status)
	}
	var input application.InboxInputError
	if _, err := kit.admin.Bulk(ctx, nil, application.BulkOperation{Mark: "read"}); !errors.As(err, &input) {
		t.Fatalf("empty selection: %v", err)
	}
	if _, err := kit.admin.Bulk(ctx, []string{"wac-1"}, application.BulkOperation{}); !errors.As(err, &input) {
		t.Fatalf("no change asked: %v", err)
	}
}

func TestScheduledSendGoesOutOnceWhenDueAndOnlyInsideTheWindow(t *testing.T) {
	kit := newInboxKit(t)
	kit.openConversation(t)
	ctx := context.Background()
	command := application.ScheduleCommand{ClientMessageID: "c-1", ConversationID: "wac-1", Text: "later", SendAt: kit.clock.now.Add(time.Hour), ActorID: "u-1"}

	var input application.InboxInputError
	soon := command
	soon.SendAt = kit.clock.now.Add(10 * time.Second)
	if _, err := kit.scheduled.Schedule(ctx, soon); !errors.As(err, &input) {
		t.Fatalf("too soon: %v", err)
	}
	view, err := kit.scheduled.Schedule(ctx, command)
	if err != nil || view.Status != "scheduled" || view.ScheduledFor == nil {
		t.Fatalf("schedule: %+v %v", view, err)
	}
	if again, _ := kit.scheduled.Schedule(ctx, command); again.ID != view.ID {
		t.Fatal("the same client id must not schedule twice")
	}

	if handled, _ := kit.scheduled.RunDue(ctx); handled != 0 || len(kit.provider.texts) != 0 {
		t.Fatal("nothing is due yet")
	}
	kit.clock.now = kit.clock.now.Add(2 * time.Hour)
	if handled, err := kit.scheduled.RunDue(ctx); err != nil || handled != 1 || fmt.Sprint(kit.provider.texts) != "[later]" {
		t.Fatalf("due: handled=%d texts=%v err=%v", handled, kit.provider.texts, err)
	}
	if handled, _ := kit.scheduled.RunDue(ctx); handled != 0 || len(kit.provider.texts) != 1 {
		t.Fatal("a sent message must not go out again")
	}
	sent, _, _ := kit.repository.FindMessageByClientID(ctx, inboxTenant, "c-1")
	if sent.Status != "sent" || sent.ProviderMessageID == "" || !sent.CreatedAt.Equal(kit.clock.now) {
		t.Fatalf("sent: %+v", sent)
	}

	late := command
	late.ClientMessageID, late.SendAt = "c-2", kit.clock.now.Add(30*time.Hour)
	if _, err := kit.scheduled.Schedule(ctx, late); err != nil {
		t.Fatal(err)
	}
	kit.clock.now = kit.clock.now.Add(31 * time.Hour)
	_, _ = kit.scheduled.RunDue(ctx)
	closed, _, _ := kit.repository.FindMessageByClientID(ctx, inboxTenant, "c-2")
	if closed.Status != "failed" || len(kit.provider.texts) != 1 {
		t.Fatalf("a send due after the window closed must fail: %+v texts=%v", closed, kit.provider.texts)
	}
	if view := application.NewMessageView(closed); view.Error == nil || view.Error.Code != application.ErrorCodeWindowLocked {
		t.Fatalf("reason: %+v", view.Error)
	}
}

func TestCancelScheduledAnnouncesDeletion(t *testing.T) {
	kit := newInboxKit(t)
	kit.openConversation(t)
	ctx := context.Background()
	view, err := kit.scheduled.Schedule(ctx, application.ScheduleCommand{ClientMessageID: "c-1", ConversationID: "wac-1", Text: "later", SendAt: kit.clock.now.Add(time.Hour), ActorID: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := kit.scheduled.Cancel(ctx, view.ID); err != nil || kit.events.count("message.deleted") != 1 {
		t.Fatalf("cancel: %v events=%v", err, kit.events.names)
	}
	var input application.InboxInputError
	if err := kit.scheduled.Cancel(ctx, view.ID); !errors.As(err, &input) {
		t.Fatalf("cancel twice: %v", err)
	}
}

func TestRetryResendsAFailedTextOnlyInsideTheWindow(t *testing.T) {
	kit := newInboxKit(t)
	kit.openConversation(t)
	ctx := context.Background()
	kit.provider.fail = errors.New("boom")
	if err := kit.send.Execute(ctx, application.PlatformSendCommand{ClientMessageID: "c-1", ConversationID: "wac-1", Text: "hi", ActorID: "u-1"}); err != nil {
		t.Fatal(err)
	}
	failed, _, _ := kit.repository.FindMessageByClientID(ctx, inboxTenant, "c-1")
	if failed.Status != "failed" {
		t.Fatalf("setup: %+v", failed)
	}

	kit.provider.fail = nil
	view, err := kit.retry.Execute(ctx, failed.ID, "u-1")
	if err != nil || view.Status != "sent" || view.Error != nil || fmt.Sprint(kit.provider.texts) != "[hi]" {
		t.Fatalf("retry: %+v err=%v texts=%v", view, err, kit.provider.texts)
	}
	var input application.InboxInputError
	if _, err := kit.retry.Execute(ctx, failed.ID, "u-1"); !errors.As(err, &input) {
		t.Fatalf("a sent message cannot be retried: %v", err)
	}

	kit.provider.fail = errors.New("boom")
	kit.clock.now = kit.clock.now.Add(time.Minute)
	if err := kit.send.Execute(ctx, application.PlatformSendCommand{ClientMessageID: "c-2", ConversationID: "wac-1", Text: "again", ActorID: "u-1"}); err != nil {
		t.Fatal(err)
	}
	second, _, _ := kit.repository.FindMessageByClientID(ctx, inboxTenant, "c-2")
	kit.provider.fail = nil
	kit.clock.now = kit.clock.now.Add(48 * time.Hour)
	if _, err := kit.retry.Execute(ctx, second.ID, "u-1"); !errors.Is(err, application.ErrWhatsAppConversationLocked) {
		t.Fatalf("retry after the window closed: %v", err)
	}
}

func TestMediaSendIsCheckedUploadedOnceAndRefusedOutsideTheWindow(t *testing.T) {
	kit := newInboxKit(t)
	kit.openConversation(t)
	ctx := context.Background()
	command := application.PlatformMediaCommand{
		ClientMessageID: "c-1", ConversationID: "wac-1", Filename: "pod.png", ContentType: "image/png",
		Data: []byte("png"), Caption: "proof", ActorID: "u-1",
	}

	var input application.InboxInputError
	bad := command
	bad.ContentType = "application/x-msdownload"
	if _, err := kit.media.Execute(ctx, bad); !errors.As(err, &input) {
		t.Fatalf("unsupported type: %v", err)
	}
	big := command
	big.Data = make([]byte, 6*1024*1024)
	if _, err := kit.media.Execute(ctx, big); !errors.As(err, &input) {
		t.Fatalf("too large: %v", err)
	}
	pathy := command
	pathy.Filename = "../pod.png"
	if _, err := kit.media.Execute(ctx, pathy); !errors.As(err, &input) {
		t.Fatalf("file name with a path: %v", err)
	}

	view, err := kit.media.Execute(ctx, command)
	if err != nil || view.MessageType != "image" || view.Status != "sent" || view.Media == nil || view.Media.URL == "" {
		t.Fatalf("send: %+v err=%v", view, err)
	}
	if kit.provider.uploads != 1 || len(kit.provider.media) != 1 || kit.provider.media[0].MediaID != "media-1" || kit.provider.media[0].Caption != "proof" {
		t.Fatalf("provider: uploads=%d media=%+v", kit.provider.uploads, kit.provider.media)
	}
	if again, err := kit.media.Execute(ctx, command); err != nil || again.ID != view.ID || kit.provider.uploads != 1 {
		t.Fatalf("replay must not upload again: %+v uploads=%d err=%v", again, kit.provider.uploads, err)
	}

	kit.clock.now = kit.clock.now.Add(48 * time.Hour)
	late := command
	late.ClientMessageID = "c-2"
	if _, err := kit.media.Execute(ctx, late); !errors.Is(err, application.ErrWhatsAppConversationLocked) {
		t.Fatalf("outside the window: %v", err)
	}
	if kit.provider.uploads != 1 {
		t.Fatal("nothing is uploaded for a refused send")
	}
}

func TestCannedRepliesNormaliseAndKeepShortcutsUnique(t *testing.T) {
	replies := application.NewCannedReplies(NewInMemoryCannedReplyRepository(), &testClock{now: inboxEpoch}, &countingIDs{})
	ctx := context.Background()

	saved, err := replies.Save(ctx, application.CannedReplyInput{Shortcut: " /ETA ", Title: "ETA", Body: "On the way", ActorID: "u-1"})
	if err != nil || saved.Shortcut != "eta" || saved.CreatedBy != "u-1" {
		t.Fatalf("save: %+v err=%v", saved, err)
	}
	if _, err := replies.Save(ctx, application.CannedReplyInput{Shortcut: "eta", Title: "Other", Body: "x"}); !errors.Is(err, application.ErrCannedReplyShortcutTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	updated, err := replies.Save(ctx, application.CannedReplyInput{ID: saved.ID, Shortcut: "eta", Title: "ETA v2", Body: "Soon"})
	if err != nil || updated.Title != "ETA v2" || updated.CreatedBy != "u-1" {
		t.Fatalf("update: %+v err=%v", updated, err)
	}
	if _, err := replies.Save(ctx, application.CannedReplyInput{ID: "nope", Shortcut: "x", Title: "x", Body: "x"}); !errors.Is(err, application.ErrCannedReplyNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	var input application.InboxInputError
	if _, err := replies.Save(ctx, application.CannedReplyInput{Shortcut: "bad shortcut!", Title: "x", Body: "x"}); !errors.As(err, &input) {
		t.Fatalf("bad shortcut: %v", err)
	}
	if err := replies.Delete(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := replies.Delete(ctx, saved.ID); !errors.Is(err, application.ErrCannedReplyNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
