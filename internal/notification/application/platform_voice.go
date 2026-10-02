package application

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

var (
	ErrVoicePromptNotFound = errors.New("ivr prompt not found")
	ErrVoiceCallNotFound   = errors.New("call not found")
	// ErrVoiceNotConfigured means no voice carrier is connected, so a call
	// cannot be placed. Everything else about voice still works.
	ErrVoiceNotConfigured = errors.New("voice calling is not connected to a carrier yet")
)

const (
	maxPromptName        = 80
	maxPromptTranslation = 500
	maxCallNotes         = 2000
	maxVoiceMessage      = 500
	defaultVoicePage     = 25
	maxVoiceStatsDays    = 90
)

type VoiceRepository interface {
	EnsureSchema(ctx context.Context) error
	ListPrompts(ctx context.Context) ([]domain.IVRPrompt, error)
	FindPrompt(ctx context.Context, id string) (domain.IVRPrompt, bool, error)
	SavePrompt(ctx context.Context, prompt domain.IVRPrompt) (domain.IVRPrompt, error)
	DeletePrompt(ctx context.Context, id string) (bool, error)
	SaveCall(ctx context.Context, call domain.VoiceCall) (domain.VoiceCall, error)
	FindCall(ctx context.Context, id string) (domain.VoiceCall, bool, error)
	ListCalls(ctx context.Context, filter domain.VoiceCallFilter) ([]domain.VoiceCall, int, error)
	CallsSince(ctx context.Context, since time.Time) ([]domain.VoiceCall, error)
}

// VoiceProvider places calls. The carrier behind it is an external system, so
// the platform ships with one that says it is not connected.
type VoiceProvider interface {
	Name() string
	Configured() bool
	InitiateCall(ctx context.Context, request VoiceCallRequest) (providerCallID string, err error)
}

type VoiceCallRequest struct {
	To       string
	Language string
	Message  string
	Record   bool
}

type IVRPromptInput struct {
	ID           string
	Name         string
	Category     string
	Translations map[string]string
	KeyPress     string
	IsActive     *bool
	ActorID      string
}

type InitiateCallCommand struct {
	To          string
	ContactName string
	Language    string
	Message     string
	Record      bool
	ActorID     string
}

type VoiceCallList struct {
	Items []domain.VoiceCall `json:"items"`
	Total int                `json:"total"`
}

type VoiceStatus struct {
	Provider   string `json:"provider"`
	Configured bool   `json:"configured"`
}

// PlatformVoice is the platform's phone line: the spoken menu, the call log
// and the dashboard over it.
type PlatformVoice struct {
	repository VoiceRepository
	provider   VoiceProvider
	clock      Clock
	ids        IDGenerator
}

func NewPlatformVoice(repository VoiceRepository, provider VoiceProvider, clock Clock, ids IDGenerator) *PlatformVoice {
	return &PlatformVoice{repository: repository, provider: provider, clock: clock, ids: ids}
}

func (u *PlatformVoice) Status() VoiceStatus {
	return VoiceStatus{Provider: u.provider.Name(), Configured: u.provider.Configured()}
}

func (u *PlatformVoice) ListPrompts(ctx context.Context) ([]domain.IVRPrompt, error) {
	return u.repository.ListPrompts(ctx)
}

func (u *PlatformVoice) SavePrompt(ctx context.Context, input IVRPromptInput) (domain.IVRPrompt, error) {
	prompt, err := validPrompt(input)
	if err != nil {
		return domain.IVRPrompt{}, err
	}
	now := u.clock.Now().UTC()
	prompt.UpdatedAt = now
	if id := strings.TrimSpace(input.ID); id != "" {
		existing, found, err := u.repository.FindPrompt(ctx, id)
		if err != nil {
			return domain.IVRPrompt{}, err
		}
		if !found {
			return domain.IVRPrompt{}, ErrVoicePromptNotFound
		}
		prompt.ID, prompt.CreatedAt, prompt.CreatedBy = existing.ID, existing.CreatedAt, existing.CreatedBy
		if input.IsActive == nil {
			prompt.IsActive = existing.IsActive
		}
		return u.repository.SavePrompt(ctx, prompt)
	}
	prompt.ID, prompt.CreatedAt, prompt.CreatedBy = u.ids.New("ivr"), now, input.ActorID
	return u.repository.SavePrompt(ctx, prompt)
}

func (u *PlatformVoice) DeletePrompt(ctx context.Context, id string) error {
	deleted, err := u.repository.DeletePrompt(ctx, strings.TrimSpace(id))
	if err == nil && !deleted {
		err = ErrVoicePromptNotFound
	}
	return err
}

func (u *PlatformVoice) ListCalls(ctx context.Context, filter domain.VoiceCallFilter) (VoiceCallList, error) {
	if filter.Limit <= 0 || filter.Limit > maxInboxPage {
		filter.Limit = defaultVoicePage
	}
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Status != "" && !domain.IsVoiceStatus(filter.Status) {
		filter.Status = ""
	}
	calls, total, err := u.repository.ListCalls(ctx, filter)
	if calls == nil {
		calls = []domain.VoiceCall{}
	}
	return VoiceCallList{Items: calls, Total: total}, err
}

func (u *PlatformVoice) GetCall(ctx context.Context, id string) (domain.VoiceCall, error) {
	call, found, err := u.repository.FindCall(ctx, strings.TrimSpace(id))
	if err == nil && !found {
		err = ErrVoiceCallNotFound
	}
	return call, err
}

func (u *PlatformVoice) UpdateNotes(ctx context.Context, id, notes string) (domain.VoiceCall, error) {
	notes = strings.TrimSpace(notes)
	if utf8.RuneCountInString(notes) > maxCallNotes || hasDisallowedControl(notes) {
		return domain.VoiceCall{}, InboxInputError("notes can be at most 2000 characters")
	}
	call, err := u.GetCall(ctx, id)
	if err != nil {
		return domain.VoiceCall{}, err
	}
	call.Notes, call.UpdatedAt = notes, u.clock.Now().UTC()
	return u.repository.SaveCall(ctx, call)
}

// Initiate places a call through the carrier and records it. Nothing is
// recorded when the platform has no carrier, so the log only ever holds calls
// that were really attempted.
func (u *PlatformVoice) Initiate(ctx context.Context, command InitiateCallCommand) (domain.VoiceCall, error) {
	to := normalizeConversationPhone(command.To)
	language := strings.ToLower(strings.TrimSpace(command.Language))
	if language == "" {
		language = "en"
	}
	message := strings.TrimSpace(command.Message)
	switch {
	case !outreachPhonePattern.MatchString(to):
		return domain.VoiceCall{}, ErrOutreachPhoneInvalid
	case !domain.IsVoiceLanguage(language):
		return domain.VoiceCall{}, InboxInputError("language must be en, ha, yo, ig or pcm")
	case utf8.RuneCountInString(message) > maxVoiceMessage || hasDisallowedControl(message):
		return domain.VoiceCall{}, InboxInputError("the spoken message can be at most 500 characters")
	case !u.provider.Configured():
		return domain.VoiceCall{}, ErrVoiceNotConfigured
	}
	providerCallID, err := u.provider.InitiateCall(ctx, VoiceCallRequest{To: to, Language: language, Message: message, Record: command.Record})
	if err != nil {
		return domain.VoiceCall{}, err
	}
	now := u.clock.Now().UTC()
	return u.repository.SaveCall(ctx, domain.VoiceCall{
		ID:             u.ids.New("call"),
		ProviderCallID: providerCallID,
		Direction:      domain.VoiceDirectionOutbound,
		ToNumber:       to,
		ContactName:    strings.TrimSpace(command.ContactName),
		Status:         domain.VoiceStatusQueued,
		Language:       language,
		StartedAt:      now,
		CreatedBy:      command.ActorID,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
}

// Stats summarises the last `days` days of calls, with an entry for every day
// so a quiet day shows as zero rather than missing.
func (u *PlatformVoice) Stats(ctx context.Context, days int) (domain.VoiceStats, error) {
	if days <= 0 {
		days = 14
	}
	if days > maxVoiceStatsDays {
		days = maxVoiceStatsDays
	}
	today := u.clock.Now().UTC().Truncate(24 * time.Hour)
	since := today.AddDate(0, 0, -(days - 1))
	calls, err := u.repository.CallsSince(ctx, since)
	if err != nil {
		return domain.VoiceStats{}, err
	}
	series := make([]string, 0, days)
	for offset := range days {
		series = append(series, since.AddDate(0, 0, offset).Format("2006-01-02"))
	}
	return domain.SummarizeVoiceCalls(calls, series), nil
}

func validPrompt(input IVRPromptInput) (domain.IVRPrompt, error) {
	name := strings.TrimSpace(input.Name)
	category := strings.ToLower(strings.TrimSpace(input.Category))
	keyPress := strings.TrimSpace(input.KeyPress)
	switch {
	case name == "" || utf8.RuneCountInString(name) > maxPromptName:
		return domain.IVRPrompt{}, InboxInputError("name must be 1 to 80 characters")
	case !domain.IsPromptCategory(category):
		return domain.IVRPrompt{}, InboxInputError("category must be greeting, menu, confirmation, error or goodbye")
	case keyPress != "" && (utf8.RuneCountInString(keyPress) != 1 || !strings.Contains("0123456789*#", keyPress)):
		return domain.IVRPrompt{}, InboxInputError("the key must be a digit, * or #")
	}
	translations, err := validTranslations(input.Translations)
	if err != nil {
		return domain.IVRPrompt{}, err
	}
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}
	return domain.IVRPrompt{Name: name, Category: category, Translations: translations, KeyPress: keyPress, IsActive: active}, nil
}

func validTranslations(raw map[string]string) (map[string]string, error) {
	translations := map[string]string{}
	for language, text := range raw {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if !domain.IsVoiceLanguage(language) {
			return nil, InboxInputError("unknown language " + language)
		}
		if utf8.RuneCountInString(text) > maxPromptTranslation || hasDisallowedControl(text) {
			return nil, InboxInputError("each translation can be at most 500 characters")
		}
		translations[language] = text
	}
	if translations["en"] == "" {
		return nil, InboxInputError("an English version is required")
	}
	return translations, nil
}
