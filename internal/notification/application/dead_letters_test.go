package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/truckmoov/services-go/pkg/eventbus"
)

type fakeDeadLetterRepository struct {
	letters  map[string]DeadLetter
	claimed  map[string]bool
	claimErr error
}

func newFakeDeadLetterRepository() *fakeDeadLetterRepository {
	return &fakeDeadLetterRepository{letters: map[string]DeadLetter{}, claimed: map[string]bool{}}
}

func (r *fakeDeadLetterRepository) EnsureSchema(context.Context) error { return nil }

func (r *fakeDeadLetterRepository) Save(_ context.Context, letter DeadLetter) (bool, error) {
	if _, exists := r.letters[letter.ID]; exists {
		return false, nil
	}
	r.letters[letter.ID] = letter
	return true, nil
}

func (r *fakeDeadLetterRepository) Find(_ context.Context, id string) (DeadLetter, bool, error) {
	letter, found := r.letters[id]
	return letter, found, nil
}

func (r *fakeDeadLetterRepository) List(_ context.Context, limit, offset int) ([]DeadLetter, int, error) {
	all := []DeadLetter{}
	for _, letter := range r.letters {
		all = append(all, letter)
	}
	return all, len(all), nil
}

func (r *fakeDeadLetterRepository) CountSince(_ context.Context, since time.Time) (int, error) {
	count := 0
	for _, letter := range r.letters {
		if !letter.FailedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

func (r *fakeDeadLetterRepository) ClaimReplay(_ context.Context, id string, _ time.Time) (bool, error) {
	if r.claimed[id] {
		return false, r.claimErr
	}
	r.claimed[id] = true
	return true, r.claimErr
}

func (r *fakeDeadLetterRepository) ReleaseReplay(_ context.Context, id string) error {
	delete(r.claimed, id)
	return nil
}

func (r *fakeDeadLetterRepository) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	var removed int64
	for id, letter := range r.letters {
		if letter.FailedAt.Before(cutoff) {
			delete(r.letters, id)
			removed++
		}
	}
	return removed, nil
}

type publishedEvent struct {
	subject  string
	envelope eventbus.EventEnvelope
}

type recordingPublisher struct {
	published []publishedEvent
	err       error
}

func (p *recordingPublisher) Publish(_ context.Context, subject string, envelope eventbus.EventEnvelope) error {
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, publishedEvent{subject, envelope})
	return nil
}

type recordingAlerts struct{ raised []string }

func (a *recordingAlerts) Raise(_ context.Context, alertType, severity, _, _ string) error {
	a.raised = append(a.raised, alertType+"/"+severity)
	return nil
}

type deadLetterKit struct {
	repository *fakeDeadLetterRepository
	publisher  *recordingPublisher
	alerts     *recordingAlerts
	clock      *movableClock
	useCase    *DeadLetters
}

func newDeadLetterKit() *deadLetterKit {
	kit := &deadLetterKit{
		repository: newFakeDeadLetterRepository(),
		publisher:  &recordingPublisher{},
		alerts:     &recordingAlerts{},
		clock:      &movableClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)},
	}
	kit.useCase = NewDeadLetters(kit.repository, kit.publisher, kit.alerts, newFakeEventClaims(), kit.clock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return kit
}

func (k *deadLetterKit) deadLetterEvent(id, subject string) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{
		ID: id, Type: "system.dlq", TenantID: "system", Subject: "tm.system.dlq.job.created", OccurredAt: k.clock.now,
		Data: map[string]any{
			"original_subject": subject,
			"attempts":         float64(5),
			"last_error":       "boom",
			"failed_at":        k.clock.now.Format(time.RFC3339),
			"original": map[string]any{
				"id": "orig-" + id, "type": "job.created", "version": float64(1), "tenant_id": "tenant-a",
				"occurred_at": "2026-10-02T11:59:00Z", "data": map[string]any{"job_id": "job-1"}, "sig": "producer-sig",
			},
		},
	}
}

func TestDeadLetterIsStoredWithItsOriginalEnvelope(t *testing.T) {
	kit := newDeadLetterKit()

	if err := kit.useCase.Handle(context.Background(), kit.deadLetterEvent("dl-1", "tm.tenant-a.job.created")); err != nil {
		t.Fatalf("handle: %v", err)
	}

	letter := kit.repository.letters["dl-1"]
	if letter.OriginalSubject != "tm.tenant-a.job.created" || letter.TenantID != "tenant-a" || letter.EventType != "job.created" ||
		letter.Attempts != 5 || letter.LastError != "boom" || letter.Envelope["id"] != "orig-dl-1" {
		t.Fatalf("unexpected letter %+v", letter)
	}
}

func TestDeadLetterFromAnotherSubjectOrTenantIsIgnored(t *testing.T) {
	kit := newDeadLetterKit()
	wrongTenant := kit.deadLetterEvent("dl-1", "tm.tenant-a.job.created")
	wrongTenant.TenantID = "tenant-a"
	wrongSubject := kit.deadLetterEvent("dl-2", "tm.tenant-a.job.created")
	wrongSubject.Subject = "tm.tenant-a.job.created"
	noOriginal := kit.deadLetterEvent("dl-3", "tm.tenant-a.job.created")
	delete(noOriginal.Data, "original")

	for _, event := range []eventbus.EventEnvelope{wrongTenant, wrongSubject, noOriginal} {
		if err := kit.useCase.Handle(context.Background(), event); err != nil {
			t.Fatalf("an event that is not ours is acknowledged, got %v", err)
		}
	}

	if len(kit.repository.letters) != 0 {
		t.Fatalf("nothing may be stored, got %+v", kit.repository.letters)
	}
}

func TestDeadLetterRedeliveryIsStoredOnce(t *testing.T) {
	kit := newDeadLetterKit()
	event := kit.deadLetterEvent("dl-1", "tm.tenant-a.job.created")

	_ = kit.useCase.Handle(context.Background(), event)
	_ = kit.useCase.Handle(context.Background(), event)

	if len(kit.repository.letters) != 1 {
		t.Fatalf("expected one letter, got %d", len(kit.repository.letters))
	}
}

func TestMoreThanTenDeadLettersInAnHourRaiseOnePlatformAlert(t *testing.T) {
	kit := newDeadLetterKit()

	for index := 0; index < 10; index++ {
		_ = kit.useCase.Handle(context.Background(), kit.deadLetterEvent("dl-"+strconv.Itoa(index), "tm.tenant-a.job.created"))
	}
	if len(kit.alerts.raised) != 0 {
		t.Fatalf("ten is not more than ten, got %v", kit.alerts.raised)
	}

	for index := 10; index < 15; index++ {
		_ = kit.useCase.Handle(context.Background(), kit.deadLetterEvent("dl-"+strconv.Itoa(index), "tm.tenant-a.job.created"))
	}

	if len(kit.alerts.raised) != 1 || kit.alerts.raised[0] != "system_errors/high" {
		t.Fatalf("expected one high system alert, got %v", kit.alerts.raised)
	}
}

func TestDeadLettersOlderThanAnHourDoNotCountTowardTheAlert(t *testing.T) {
	kit := newDeadLetterKit()
	for index := 0; index < 12; index++ {
		event := kit.deadLetterEvent("old-"+strconv.Itoa(index), "tm.tenant-a.job.created")
		event.Data["failed_at"] = kit.clock.now.Add(-3 * time.Hour).Format(time.RFC3339)
		_ = kit.useCase.Handle(context.Background(), event)
	}

	if len(kit.alerts.raised) != 0 {
		t.Fatalf("old failures are not a burst, got %v", kit.alerts.raised)
	}
}

func TestReplayPublishesTheOriginalEventToItsOriginalSubjectOnce(t *testing.T) {
	kit := newDeadLetterKit()
	_ = kit.useCase.Handle(context.Background(), kit.deadLetterEvent("dl-1", "tm.tenant-a.job.created"))

	letter, err := kit.useCase.Replay(context.Background(), "dl-1")

	if err != nil || letter.ReplayedAt == nil {
		t.Fatalf("replay: %+v err=%v", letter, err)
	}
	if len(kit.publisher.published) != 1 {
		t.Fatalf("expected one publish, got %d", len(kit.publisher.published))
	}
	published := kit.publisher.published[0]
	if published.subject != "tm.tenant-a.job.created" || published.envelope.ID != "orig-dl-1" || published.envelope.TenantID != "tenant-a" ||
		published.envelope.Signature != "producer-sig" || published.envelope.Data["job_id"] != "job-1" {
		t.Fatalf("unexpected replay %+v", published)
	}

	if _, err := kit.useCase.Replay(context.Background(), "dl-1"); !errors.Is(err, ErrDeadLetterAlreadyReplayed) {
		t.Fatalf("expected a second replay to be refused, got %v", err)
	}
	if len(kit.publisher.published) != 1 {
		t.Fatal("a second replay must not publish")
	}
}

func TestReplayOfAnUnknownDeadLetterIsNotFound(t *testing.T) {
	kit := newDeadLetterKit()

	if _, err := kit.useCase.Replay(context.Background(), "missing"); !errors.Is(err, ErrDeadLetterNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestFailedReplayCanBeTriedAgain(t *testing.T) {
	kit := newDeadLetterKit()
	_ = kit.useCase.Handle(context.Background(), kit.deadLetterEvent("dl-1", "tm.tenant-a.job.created"))
	kit.publisher.err = errors.New("nats down")

	if _, err := kit.useCase.Replay(context.Background(), "dl-1"); err == nil {
		t.Fatal("expected the publish failure")
	}
	kit.publisher.err = nil

	if _, err := kit.useCase.Replay(context.Background(), "dl-1"); err != nil || len(kit.publisher.published) != 1 {
		t.Fatalf("expected the retry to publish, got err=%v published=%d", err, len(kit.publisher.published))
	}
}

func TestReplayRefusesASubjectThatWouldLoopTheDeadLetterQueue(t *testing.T) {
	kit := newDeadLetterKit()
	_ = kit.useCase.Handle(context.Background(), kit.deadLetterEvent("dl-1", "tm.system.dlq.job.created"))
	_ = kit.useCase.Handle(context.Background(), kit.deadLetterEvent("dl-2", "other.subject"))

	for _, id := range []string{"dl-1", "dl-2"} {
		if _, err := kit.useCase.Replay(context.Background(), id); !errors.Is(err, ErrDeadLetterNotReplayable) {
			t.Fatalf("%s: expected not replayable, got %v", id, err)
		}
	}
	if len(kit.publisher.published) != 0 || len(kit.repository.claimed) != 0 {
		t.Fatal("a refused replay must not publish or mark the letter")
	}
}

func TestPurgeDropsDeadLettersPastThirtyDays(t *testing.T) {
	kit := newDeadLetterKit()
	old := kit.deadLetterEvent("old", "tm.tenant-a.job.created")
	old.Data["failed_at"] = kit.clock.now.Add(-31 * 24 * time.Hour).Format(time.RFC3339)
	recent := kit.deadLetterEvent("recent", "tm.tenant-a.job.created")
	recent.Data["failed_at"] = kit.clock.now.Add(-29 * 24 * time.Hour).Format(time.RFC3339)
	_ = kit.useCase.Handle(context.Background(), old)
	_ = kit.useCase.Handle(context.Background(), recent)

	removed, err := kit.useCase.Purge(context.Background())

	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if _, kept := kit.repository.letters["recent"]; !kept || len(kit.repository.letters) != 1 {
		t.Fatalf("expected only the recent letter to stay, got %+v", kit.repository.letters)
	}
}

func TestForgedDeadLetterIsNotStoredAndASignedOneIs(t *testing.T) {
	const subject = "tm.system.dlq.job.created"
	kit := newDeadLetterKit()
	handler := verifiedBy(kit.useCase.Handle)
	forged := deliveredOn(t, subject, kit.deadLetterEvent("dl-forged", "tm.tenant-a.job.created"), false)
	genuine := deliveredOn(t, subject, kit.deadLetterEvent("dl-genuine", "tm.tenant-a.job.created"), true)

	for _, event := range []eventbus.EventEnvelope{forged, genuine} {
		if err := handler(context.Background(), event); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}

	if _, stored := kit.repository.letters["dl-forged"]; stored {
		t.Fatal("an unsigned dead letter must not be stored")
	}
	if _, stored := kit.repository.letters["dl-genuine"]; !stored {
		t.Fatal("a signed dead letter must be stored")
	}
}
