package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
)

type fakeCarrier struct {
	configured bool
	requests   []application.VoiceCallRequest
}

func (c *fakeCarrier) Name() string     { return "fake" }
func (c *fakeCarrier) Configured() bool { return c.configured }
func (c *fakeCarrier) InitiateCall(_ context.Context, request application.VoiceCallRequest) (string, error) {
	c.requests = append(c.requests, request)
	return fmt.Sprintf("carrier-%d", len(c.requests)), nil
}

func newVoice(provider application.VoiceProvider) (*application.PlatformVoice, *InMemoryVoiceRepository, *testClock) {
	repository := NewInMemoryVoiceRepository()
	clock := &testClock{now: inboxEpoch}
	return application.NewPlatformVoice(repository, provider, clock, &countingIDs{}), repository, clock
}

func TestIVRPromptsNeedEnglishAndKnownCategoriesKeysAndLanguages(t *testing.T) {
	voice, _, _ := newVoice(NullVoiceProvider{})
	ctx := context.Background()
	var input application.InboxInputError

	saved, err := voice.SavePrompt(ctx, application.IVRPromptInput{
		Name: " Welcome ", Category: "Greeting", KeyPress: "1", Translations: map[string]string{"en": " Hello ", "ha": "Sannu", "yo": "  "}, ActorID: "u-1",
	})
	if err != nil || saved.Category != "greeting" || !saved.IsActive || fmt.Sprint(len(saved.Translations)) != "2" || saved.Translations["en"] != "Hello" {
		t.Fatalf("save: %+v err=%v", saved, err)
	}
	for label, bad := range map[string]application.IVRPromptInput{
		"no english":   {Name: "a", Category: "menu", Translations: map[string]string{"ha": "Sannu"}},
		"bad category": {Name: "a", Category: "music", Translations: map[string]string{"en": "x"}},
		"bad key":      {Name: "a", Category: "menu", KeyPress: "12", Translations: map[string]string{"en": "x"}},
		"bad language": {Name: "a", Category: "menu", Translations: map[string]string{"en": "x", "fr": "y"}},
		"no name":      {Name: " ", Category: "menu", Translations: map[string]string{"en": "x"}},
	} {
		if _, err := voice.SavePrompt(ctx, bad); !errors.As(err, &input) {
			t.Errorf("%s: %v", label, err)
		}
	}

	off := false
	updated, err := voice.SavePrompt(ctx, application.IVRPromptInput{ID: saved.ID, Name: "Welcome 2", Category: "greeting", Translations: map[string]string{"en": "Hi"}, IsActive: &off})
	if err != nil || updated.IsActive || updated.CreatedBy != "u-1" {
		t.Fatalf("update: %+v err=%v", updated, err)
	}
	if _, err := voice.SavePrompt(ctx, application.IVRPromptInput{ID: "nope", Name: "a", Category: "menu", Translations: map[string]string{"en": "x"}}); !errors.Is(err, application.ErrVoicePromptNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if err := voice.DeletePrompt(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := voice.DeletePrompt(ctx, saved.ID); !errors.Is(err, application.ErrVoicePromptNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestACallIsOnlyRecordedWhenACarrierPlacedIt(t *testing.T) {
	ctx := context.Background()
	voice, _, _ := newVoice(NullVoiceProvider{})
	if status := voice.Status(); status.Configured || status.Provider != "none" {
		t.Fatalf("status: %+v", status)
	}
	if _, err := voice.Initiate(ctx, application.InitiateCallCommand{To: "2348012345678"}); !errors.Is(err, application.ErrVoiceNotConfigured) {
		t.Fatalf("no carrier: %v", err)
	}
	if list, _ := voice.ListCalls(ctx, domain.VoiceCallFilter{}); list.Total != 0 {
		t.Fatalf("nothing may be logged: %+v", list)
	}

	carrier := &fakeCarrier{configured: true}
	voice, _, _ = newVoice(carrier)
	var input application.InboxInputError
	if _, err := voice.Initiate(ctx, application.InitiateCallCommand{To: "12"}); !errors.Is(err, application.ErrOutreachPhoneInvalid) {
		t.Fatalf("bad number: %v", err)
	}
	if _, err := voice.Initiate(ctx, application.InitiateCallCommand{To: "2348012345678", Language: "fr"}); !errors.As(err, &input) {
		t.Fatalf("bad language: %v", err)
	}
	call, err := voice.Initiate(ctx, application.InitiateCallCommand{To: "08012345678", ContactName: "Ada", Message: "Hello", Record: true, ActorID: "u-1"})
	if err != nil || call.Status != "queued" || call.ProviderCallID != "carrier-1" || call.Language != "en" || call.ToNumber != "08012345678" && call.ToNumber != "2348012345678" {
		t.Fatalf("call: %+v err=%v", call, err)
	}
	if len(carrier.requests) != 1 || !carrier.requests[0].Record || carrier.requests[0].Message != "Hello" {
		t.Fatalf("carrier saw: %+v", carrier.requests)
	}
}

func TestCallListFiltersNotesAndDashboard(t *testing.T) {
	ctx := context.Background()
	voice, repository, clock := newVoice(NullVoiceProvider{})
	clock.now = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	seed := func(id, status, direction, language, recording string, day int, seconds int) {
		started := time.Date(2026, 10, day, 9, 0, 0, 0, time.UTC)
		if _, err := repository.SaveCall(ctx, domain.VoiceCall{
			ID: id, Direction: direction, Status: status, Language: language, ToNumber: "2348000000001", ContactName: "Ada " + id,
			StartedAt: started, DurationSeconds: seconds, RecordingURL: recording, CreatedAt: started, UpdatedAt: started,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("a", "completed", "outbound", "en", "https://rec/a", 3, 60)
	seed("b", "completed", "inbound", "ha", "", 3, 120)
	seed("c", "missed", "inbound", "en", "", 2, 0)
	seed("d", "failed", "outbound", "en", "", 1, 0)
	seed("old", "completed", "outbound", "en", "", 0, 30)

	all, _ := voice.ListCalls(ctx, domain.VoiceCallFilter{})
	if all.Total != 5 || all.Items[0].ID != "b" && all.Items[0].ID != "a" {
		t.Fatalf("all: %+v", all)
	}
	for label, check := range map[string]struct {
		filter domain.VoiceCallFilter
		want   int
	}{
		"direction": {domain.VoiceCallFilter{Direction: "inbound"}, 2},
		"status":    {domain.VoiceCallFilter{Status: "completed"}, 3},
		"recording": {domain.VoiceCallFilter{HasRecording: true}, 1},
		"search":    {domain.VoiceCallFilter{Query: "ada c"}, 1},
		"bad":       {domain.VoiceCallFilter{Status: "nonsense"}, 5},
	} {
		if got, _ := voice.ListCalls(ctx, check.filter); got.Total != check.want {
			t.Errorf("%s: %d, want %d", label, got.Total, check.want)
		}
	}

	if call, err := voice.UpdateNotes(ctx, "a", "  asked for a callback "); err != nil || call.Notes != "asked for a callback" {
		t.Fatalf("notes: %+v err=%v", call, err)
	}
	if _, err := voice.UpdateNotes(ctx, "nope", "x"); !errors.Is(err, application.ErrVoiceCallNotFound) {
		t.Fatalf("unknown call: %v", err)
	}

	stats, err := voice.Stats(ctx, 3)
	if err != nil || stats.Total != 4 || stats.Completed != 2 || stats.Missed != 1 || stats.Failed != 1 {
		t.Fatalf("stats: %+v err=%v", stats, err)
	}
	if stats.AnswerRate != 50 || stats.AverageDuration != 90 || stats.ByLanguage["ha"] != 1 || len(stats.Days) != 3 {
		t.Fatalf("derived: %+v", stats)
	}
	if stats.Days[0].Day != "2026-10-01" || stats.Days[2].Day != "2026-10-03" || stats.Days[2].Completed != 2 || stats.Days[1].Missed != 1 {
		t.Fatalf("days: %+v", stats.Days)
	}
}
