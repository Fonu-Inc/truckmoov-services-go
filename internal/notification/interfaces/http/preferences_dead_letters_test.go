package notificationhttp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/application"
	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
	httpx "github.com/truckmoov/services-go/pkg/http"
	"github.com/truckmoov/services-go/pkg/identity"
)

type memoryPreferences struct {
	stored map[string]domain.NotificationPreferences
}

func (m *memoryPreferences) EnsureSchema(context.Context) error { return nil }

func (m *memoryPreferences) FindPreferences(_ context.Context, tenantID string) (domain.NotificationPreferences, bool, error) {
	preferences, found := m.stored[tenantID]
	return preferences, found, nil
}

func (m *memoryPreferences) SavePreferences(_ context.Context, preferences domain.NotificationPreferences) error {
	m.stored[preferences.TenantID] = preferences
	return nil
}

func preferencesServer(t *testing.T) http.Handler {
	t.Helper()
	repository := &memoryPreferences{stored: map[string]domain.NotificationPreferences{}}
	useCase := application.NewNotificationPreferencesUseCase(repository, inboxClock{time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)})
	mux := http.NewServeMux()
	(&Handler{}).WithPreferences(useCase).RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	return httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))
}

func callPreferences(server http.Handler, method, tenantID, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, makeNotificationAuthRequest(method, "/notify/preferences", []byte(body), tenantID))
	return recorder
}

func decodePreferences(t *testing.T, recorder *httptest.ResponseRecorder) domain.NotificationPreferences {
	t.Helper()
	var preferences domain.NotificationPreferences
	if err := json.Unmarshal(decodeNotificationEnvelope(t, recorder).Data, &preferences); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	return preferences
}

func TestPreferencesDefaultToEmailOnAndTheRestOff(t *testing.T) {
	recorder := callPreferences(preferencesServer(t), http.MethodGet, "tenant-a", "")

	preferences := decodePreferences(t, recorder)
	if recorder.Code != http.StatusOK || !preferences.Email || preferences.WhatsApp || preferences.SMS || preferences.TenantID != "tenant-a" {
		t.Fatalf("unexpected defaults %d %+v", recorder.Code, preferences)
	}
}

func TestPreferencesPutIsScopedToTheTokenTenant(t *testing.T) {
	server := preferencesServer(t)

	put := callPreferences(server, http.MethodPut, "tenant-a", `{"email":true,"whatsapp":true,"sms":false}`)
	mine := decodePreferences(t, callPreferences(server, http.MethodGet, "tenant-a", ""))
	other := decodePreferences(t, callPreferences(server, http.MethodGet, "tenant-b", ""))

	if put.Code != http.StatusOK || !mine.WhatsApp {
		t.Fatalf("expected the saved choice, got %d %+v", put.Code, mine)
	}
	if other.WhatsApp {
		t.Fatalf("another tenant must keep the defaults, got %+v", other)
	}
}

func TestPreferencesPutRefusesEverythingOffAndUnknownFields(t *testing.T) {
	server := preferencesServer(t)

	if got := callPreferences(server, http.MethodPut, "tenant-a", `{"email":false,"whatsapp":false,"sms":false}`).Code; got != http.StatusBadRequest {
		t.Errorf("all off: %d", got)
	}
	if got := callPreferences(server, http.MethodPut, "tenant-a", `{"email":true,"tenant_id":"tenant-b"}`).Code; got != http.StatusBadRequest {
		t.Errorf("a tenant in the body must be refused: %d", got)
	}
}

func TestPreferencesRequireSignIn(t *testing.T) {
	if got := callPreferences(preferencesServer(t), http.MethodGet, "", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", got)
	}
}

type memoryDeadLetters struct {
	letters map[string]application.DeadLetter
}

func (m *memoryDeadLetters) EnsureSchema(context.Context) error { return nil }

func (m *memoryDeadLetters) Save(_ context.Context, letter application.DeadLetter) (bool, error) {
	m.letters[letter.ID] = letter
	return true, nil
}

func (m *memoryDeadLetters) Find(_ context.Context, id string) (application.DeadLetter, bool, error) {
	letter, found := m.letters[id]
	return letter, found, nil
}

func (m *memoryDeadLetters) List(context.Context, int, int) ([]application.DeadLetter, int, error) {
	all := []application.DeadLetter{}
	for _, letter := range m.letters {
		all = append(all, letter)
	}
	return all, len(all), nil
}

func (m *memoryDeadLetters) CountSince(context.Context, time.Time) (int, error) {
	return len(m.letters), nil
}

func (m *memoryDeadLetters) ClaimReplay(_ context.Context, id string, at time.Time) (bool, error) {
	letter := m.letters[id]
	if letter.ReplayedAt != nil {
		return false, nil
	}
	letter.ReplayedAt = &at
	m.letters[id] = letter
	return true, nil
}

func (m *memoryDeadLetters) ReleaseReplay(_ context.Context, id string) error {
	letter := m.letters[id]
	letter.ReplayedAt = nil
	m.letters[id] = letter
	return nil
}

func (m *memoryDeadLetters) DeleteOlderThan(context.Context, time.Time) (int64, error) { return 0, nil }

type replaySink struct{ subjects []string }

func (s *replaySink) Publish(_ context.Context, subject string, _ eventbus.EventEnvelope) error {
	s.subjects = append(s.subjects, subject)
	return nil
}

func deadLetterServer(t *testing.T) (http.Handler, *replaySink) {
	t.Helper()
	repository := &memoryDeadLetters{letters: map[string]application.DeadLetter{
		"dl-1": {
			ID: "dl-1", OriginalSubject: "tm.tenant-a.job.created", TenantID: "tenant-a", EventType: "job.created", Attempts: 5, LastError: "boom",
			FailedAt: time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC),
			Envelope: map[string]any{"id": "orig-1", "type": "job.created", "version": float64(1), "tenant_id": "tenant-a", "data": map[string]any{}},
		},
	}}
	sink := &replaySink{}
	useCase := application.NewDeadLetters(repository, sink, nil, nil, inboxClock{time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	(&Handler{}).WithDeadLetters(useCase).RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	return httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me")), sink
}

func callDeadLetters(server http.Handler, method, path, tenantID, role string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, makeNotificationAuthRequestWithRole(method, path, nil, tenantID, role))
	return recorder
}

func TestDeadLetterRoutesAreForPlatformStaffOnly(t *testing.T) {
	server, sink := deadLetterServer(t)

	for _, path := range []string{"/notify/system/dead-letters", "/notify/system/dead-letters/dl-1/replay"} {
		method := http.MethodGet
		if path != "/notify/system/dead-letters" {
			method = http.MethodPost
		}
		if got := callDeadLetters(server, method, path, "", "").Code; got != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", path, got)
		}
		if got := callDeadLetters(server, method, path, "tenant-a", "FLEET_ADMIN").Code; got != http.StatusForbidden {
			t.Errorf("%s tenant admin: %d", path, got)
		}
		if got := callDeadLetters(server, method, path, "system", "GOVERNANCE_OFFICER").Code; got != http.StatusForbidden {
			t.Errorf("%s staff without messaging: %d", path, got)
		}
	}
	if len(sink.subjects) != 0 {
		t.Fatalf("a refused caller must not replay anything, got %v", sink.subjects)
	}
}

func TestDeadLetterListShowsWhatFailedWithoutTheEnvelope(t *testing.T) {
	server, _ := deadLetterServer(t)

	recorder := callDeadLetters(server, http.MethodGet, "/notify/system/dead-letters", "system", "SUPER_ADMIN")

	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(decodeNotificationEnvelope(t, recorder).Data, &page); err != nil || recorder.Code != http.StatusOK || page.Total != 1 {
		t.Fatalf("list: %d %s err=%v", recorder.Code, recorder.Body.String(), err)
	}
	item := page.Items[0]
	if item["id"] != "dl-1" || item["original_subject"] != "tm.tenant-a.job.created" || item["last_error"] != "boom" || item["attempts"] != float64(5) {
		t.Fatalf("unexpected item %v", item)
	}
	if _, leaked := item["envelope"]; leaked {
		t.Fatal("the event itself is not listed")
	}
}

func TestDeadLetterReplayWorksOnceAndThenConflicts(t *testing.T) {
	server, sink := deadLetterServer(t)

	first := callDeadLetters(server, http.MethodPost, "/notify/system/dead-letters/dl-1/replay", "system", "SUPER_ADMIN")
	second := callDeadLetters(server, http.MethodPost, "/notify/system/dead-letters/dl-1/replay", "system", "SUPER_ADMIN")
	missing := callDeadLetters(server, http.MethodPost, "/notify/system/dead-letters/nope/replay", "system", "SUPER_ADMIN")

	if first.Code != http.StatusOK || second.Code != http.StatusConflict || missing.Code != http.StatusNotFound {
		t.Fatalf("expected 200, 409, 404, got %d, %d, %d", first.Code, second.Code, missing.Code)
	}
	if len(sink.subjects) != 1 || sink.subjects[0] != "tm.tenant-a.job.created" {
		t.Fatalf("expected one publish to the original subject, got %v", sink.subjects)
	}
}

func TestDeadLetterRoutesAreNotConfiguredWithoutTheUseCase(t *testing.T) {
	mux := http.NewServeMux()
	(&Handler{}).RegisterRoutes(httpx.NewRoutes(mux, false, nil, nil))
	server := httpx.WithAuthContext(mux, identity.SharedSecretVerifier("replace-me"))

	if got := callDeadLetters(server, http.MethodGet, "/notify/system/dead-letters", "system", "SUPER_ADMIN").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", got)
	}
}
