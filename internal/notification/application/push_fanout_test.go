package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
	"github.com/truckmoov/services-go/pkg/eventbus"
)

type sentPush struct {
	tenantID, userID string
	message          domain.PushMessage
}

type recordingPushProvider struct {
	sent []sentPush
	err  error
}

func (p *recordingPushProvider) Send(_ context.Context, tenantID, channel, recipient, content string) (string, error) {
	if channel != domain.ChannelPush {
		return domain.StatusFailed, errors.New("wrong channel " + channel)
	}
	if p.err != nil {
		return domain.StatusFailed, p.err
	}
	var message domain.PushMessage
	_ = json.Unmarshal([]byte(content), &message)
	p.sent = append(p.sent, sentPush{tenantID: tenantID, userID: recipient, message: message})
	return domain.StatusSent, nil
}

type stubRecipients struct{ users []PushRecipient }

func (s stubRecipients) Recipients(context.Context, string) ([]PushRecipient, error) {
	return s.users, nil
}

type stubScope struct {
	vehicles    []string
	wholeTenant bool
	err         error
}

func (s stubScope) VehicleScope(context.Context, string, string) ([]string, bool, error) {
	return s.vehicles, s.wholeTenant, s.err
}

type stubDevices struct{ users map[string]bool }

func (stubDevices) SaveDevice(context.Context, domain.PushDevice) error            { return nil }
func (stubDevices) DeleteUserDevice(context.Context, string, string, string) error { return nil }
func (stubDevices) DeleteToken(context.Context, string) error                      { return nil }
func (stubDevices) TokensFor(context.Context, string, string) ([]string, error)    { return nil, nil }
func (s stubDevices) UsersWithDevices(context.Context, string) (map[string]bool, error) {
	return s.users, nil
}

type fakePushCounter struct{ counts map[string]int64 }

func (c *fakePushCounter) Increment(_ context.Context, key string, _ time.Duration) (int64, error) {
	c.counts[key]++
	return c.counts[key], nil
}

type pushHarness struct {
	fanout   *PushFanout
	provider *recordingPushProvider
}

func newPushHarness(users []PushRecipient, scope stubScope) pushHarness {
	provider := &recordingPushProvider{}
	send := NewSendMessageUseCase(newFakeOutboxRepository(), provider, &fakeDeliveryPublisher{}, nil, &recordingCharger{},
		fixedNotificationClock{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}, fixedNotificationIDGenerator{id: "1"}, "default")
	withDevices := map[string]bool{}
	for _, user := range users {
		withDevices[user.ID] = true
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	fanout := NewPushFanout(send, stubRecipients{users: users}, scope, stubDevices{users: withDevices}, newFakeEventClaims(),
		&fakePushCounter{counts: map[string]int64{}},
		PushLimits{CoalesceWindow: 10 * time.Minute, UserCap: 8, UserCapWindow: 10 * time.Minute}, logger)
	return pushHarness{fanout: fanout, provider: provider}
}

func dispatcher(id string) PushRecipient { return PushRecipient{ID: id, Role: "DISPATCHER"} }

func envelope(id, eventType string, data map[string]any) eventbus.EventEnvelope {
	return eventbus.EventEnvelope{ID: id, Type: eventType, TenantID: "tenant-a", Data: data}
}

func alertEnvelope(id, alertType, severity, vehicleID string) eventbus.EventEnvelope {
	return envelope(id, "alert.triggered", map[string]any{
		"alert_id": "alert-" + id, "alert_type": alertType, "severity": severity, "vehicle_id": vehicleID,
	})
}

func TestPushMapsEachEventToItsKindAndID(t *testing.T) {
	cases := []struct {
		name     string
		handle   func(*PushFanout) func(context.Context, eventbus.EventEnvelope) error
		event    eventbus.EventEnvelope
		kind, id string
	}{
		{"alert", func(f *PushFanout) func(context.Context, eventbus.EventEnvelope) error { return f.HandleAlert },
			alertEnvelope("e1", "road_speeding", "high", "veh-1"), domain.PushKindAlert, "alert-e1"},
		{"alert without id falls back to vehicle", func(f *PushFanout) func(context.Context, eventbus.EventEnvelope) error { return f.HandleAlert },
			envelope("e2", "alert.triggered", map[string]any{"alert_type": "eta_breach", "severity": "medium", "vehicle_id": "veh-2"}), domain.PushKindAlert, "veh-2"},
		{"job assigned", func(f *PushFanout) func(context.Context, eventbus.EventEnvelope) error { return f.HandleJobStatus },
			envelope("e3", "job.status.changed", map[string]any{"job_id": "job-1", "new_status": "assigned"}), domain.PushKindJob, "job-1"},
		{"job delivered", func(f *PushFanout) func(context.Context, eventbus.EventEnvelope) error { return f.HandleJobStatus },
			envelope("e4", "job.status.changed", map[string]any{"job_id": "job-2", "new_status": "delivered"}), domain.PushKindJob, "job-2"},
		{"job cancelled", func(f *PushFanout) func(context.Context, eventbus.EventEnvelope) error { return f.HandleJobStatus },
			envelope("e5", "job.status.changed", map[string]any{"job_id": "job-3", "new_status": "cancelled"}), domain.PushKindJob, "job-3"},
		{"offer created", func(f *PushFanout) func(context.Context, eventbus.EventEnvelope) error { return f.HandleJobOffer },
			envelope("e6", "job.offer.created", map[string]any{"job_id": "job-4", "offer_id": "off-1", "status": "pending"}), domain.PushKindJobOffer, "job-4"},
		{"offer responded", func(f *PushFanout) func(context.Context, eventbus.EventEnvelope) error { return f.HandleJobOffer },
			envelope("e7", "job.offer.responded", map[string]any{"job_id": "job-5", "offer_id": "off-2", "status": "accepted"}), domain.PushKindJobOffer, "job-5"},
		{"whatsapp message", func(f *PushFanout) func(context.Context, eventbus.EventEnvelope) error { return f.HandleActivity },
			envelope("e8", "activity.logged", map[string]any{"action": "whatsapp.message.received", "metadata": map[string]any{"conversation_id": "conv-1", "preview": "On my way"}}), domain.PushKindConversation, "conv-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPushHarness([]PushRecipient{dispatcher("u1")}, stubScope{})
			if err := tc.handle(h.fanout)(context.Background(), tc.event); err != nil {
				t.Fatal(err)
			}
			if len(h.provider.sent) != 1 {
				t.Fatalf("want one push, got %d", len(h.provider.sent))
			}
			got := h.provider.sent[0].message
			if got.Kind != tc.kind || got.ID != tc.id || got.Title == "" || got.Body == "" || got.CollapseKey == "" {
				t.Fatalf("unexpected push %+v", got)
			}
		})
	}
}

func TestPushIgnoresEventsNotWorthAPush(t *testing.T) {
	h := newPushHarness([]PushRecipient{dispatcher("u1")}, stubScope{})
	ctx := context.Background()
	_ = h.fanout.HandleJobStatus(ctx, envelope("e1", "job.status.changed", map[string]any{"job_id": "j", "new_status": "en-route"}))
	_ = h.fanout.HandleActivity(ctx, envelope("e2", "activity.logged", map[string]any{"action": "job.created"}))
	_ = h.fanout.HandleAlert(ctx, alertEnvelope("e3", "geofence_entry", "low", "veh-1"))
	_ = h.fanout.HandleAlert(ctx, alertEnvelope("e4", "geofence_entry", "", "veh-1"))
	if len(h.provider.sent) != 0 {
		t.Fatalf("want no pushes, got %+v", h.provider.sent)
	}
}

func TestPushHonoursEachPreference(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		preference string
		send       func(*PushFanout) error
	}{
		{"pushAlerts", func(f *PushFanout) error {
			return f.HandleAlert(ctx, alertEnvelope("e1", "road_speeding", "high", "v"))
		}},
		{"geofenceAlerts", func(f *PushFanout) error {
			return f.HandleAlert(ctx, alertEnvelope("e1", "geofence_exit", "high", "v"))
		}},
		{"incidentAlerts", func(f *PushFanout) error {
			return f.HandleAlert(ctx, alertEnvelope("e1", "route_deviation", "high", "v"))
		}},
		{"jobAssignment", func(f *PushFanout) error {
			return f.HandleJobStatus(ctx, envelope("e1", "job.status.changed", map[string]any{"job_id": "j", "new_status": "assigned"}))
		}},
		{"jobAssignment", func(f *PushFanout) error {
			return f.HandleJobOffer(ctx, envelope("e1", "job.offer.created", map[string]any{"job_id": "j"}))
		}},
		{"jobCompletion", func(f *PushFanout) error {
			return f.HandleJobStatus(ctx, envelope("e1", "job.status.changed", map[string]any{"job_id": "j", "new_status": "delivered"}))
		}},
		{"jobCompletion", func(f *PushFanout) error {
			return f.HandleJobStatus(ctx, envelope("e1", "job.status.changed", map[string]any{"job_id": "j", "new_status": "cancelled"}))
		}},
	}
	for _, tc := range cases {
		off := PushRecipient{ID: "off", Role: "DISPATCHER", Preferences: map[string]bool{tc.preference: false}}
		on := PushRecipient{ID: "on", Role: "DISPATCHER", Preferences: map[string]bool{tc.preference: true}}
		h := newPushHarness([]PushRecipient{off, on}, stubScope{})
		if err := tc.send(h.fanout); err != nil {
			t.Fatal(err)
		}
		if len(h.provider.sent) != 1 || h.provider.sent[0].userID != "on" {
			t.Fatalf("%s off must stop the push: %+v", tc.preference, h.provider.sent)
		}
	}
	// geofenceAlerts off leaves other incidents alone.
	h := newPushHarness([]PushRecipient{{ID: "u", Role: "DISPATCHER", Preferences: map[string]bool{"geofenceAlerts": false}}}, stubScope{})
	_ = h.fanout.HandleAlert(ctx, alertEnvelope("e9", "road_speeding", "high", "v"))
	if len(h.provider.sent) != 1 {
		t.Fatal("geofenceAlerts must not silence incident alerts")
	}
}

func TestOwnerOperatorOnlyHearsAboutVehiclesInScope(t *testing.T) {
	ctx := context.Background()
	operator := PushRecipient{ID: "oo", Role: "OWNER_OPERATOR"}
	cases := []struct {
		name  string
		scope stubScope
		want  int
	}{
		{"in scope", stubScope{vehicles: []string{"veh-1"}}, 1},
		{"out of scope", stubScope{vehicles: []string{"veh-9"}}, 0},
		{"whole tenant", stubScope{wholeTenant: true}, 1},
		{"unresolved scope fails closed", stubScope{err: errors.New("fleet down")}, 0},
	}
	for _, tc := range cases {
		h := newPushHarness([]PushRecipient{operator}, tc.scope)
		_ = h.fanout.HandleAlert(ctx, alertEnvelope("e1", "road_speeding", "high", "veh-1"))
		if len(h.provider.sent) != tc.want {
			t.Fatalf("%s: want %d pushes, got %d", tc.name, tc.want, len(h.provider.sent))
		}
	}

	h := newPushHarness([]PushRecipient{operator}, stubScope{wholeTenant: true})
	_ = h.fanout.HandleJobStatus(ctx, envelope("e2", "job.status.changed", map[string]any{"job_id": "j", "new_status": "assigned"}))
	_ = h.fanout.HandleJobOffer(ctx, envelope("e3", "job.offer.created", map[string]any{"job_id": "j"}))
	if len(h.provider.sent) != 0 {
		t.Fatalf("an event with no vehicle is tenant-wide and only for ops roles: %+v", h.provider.sent)
	}
}

func TestConversationPushGoesToMessagingRoles(t *testing.T) {
	h := newPushHarness([]PushRecipient{{ID: "fin", Role: "FINANCE_VIEWER"}, {ID: "oo", Role: "OWNER_OPERATOR"}, dispatcher("d")}, stubScope{})
	_ = h.fanout.HandleActivity(context.Background(), envelope("e1", "activity.logged", map[string]any{
		"action": "whatsapp.message.received", "metadata": map[string]any{"conversation_id": "c1"},
	}))
	got := map[string]bool{}
	for _, push := range h.provider.sent {
		got[push.userID] = true
	}
	if len(got) != 2 || !got["oo"] || !got["d"] {
		t.Fatalf("want owner operator and dispatcher only, got %v", got)
	}
}

func TestPushIsSentOncePerEventAndUser(t *testing.T) {
	h := newPushHarness([]PushRecipient{dispatcher("u1")}, stubScope{})
	event := alertEnvelope("e1", "road_speeding", "high", "veh-1")
	_ = h.fanout.HandleAlert(context.Background(), event)
	_ = h.fanout.HandleAlert(context.Background(), event)
	if len(h.provider.sent) != 1 {
		t.Fatalf("redelivery must not push twice, got %d", len(h.provider.sent))
	}
}

func TestPushSkipsTheActor(t *testing.T) {
	h := newPushHarness([]PushRecipient{dispatcher("actor"), dispatcher("other")}, stubScope{})
	event := envelope("e1", "job.status.changed", map[string]any{"job_id": "j", "new_status": "assigned"})
	event.Actor = map[string]any{"id": "actor"}
	_ = h.fanout.HandleJobStatus(context.Background(), event)
	if len(h.provider.sent) != 1 || h.provider.sent[0].userID != "other" {
		t.Fatalf("the actor must not be told about their own action: %+v", h.provider.sent)
	}
}

func TestPushSkipsUsersWithoutADevice(t *testing.T) {
	h := newPushHarness([]PushRecipient{dispatcher("u1")}, stubScope{})
	h.fanout.devices = stubDevices{users: map[string]bool{}}
	_ = h.fanout.HandleAlert(context.Background(), alertEnvelope("e1", "road_speeding", "high", "veh-1"))
	if len(h.provider.sent) != 0 {
		t.Fatal("a user with no app install has nothing to push to")
	}
}

func TestPushCoalescesRepeatsOfTheSameKey(t *testing.T) {
	h := newPushHarness([]PushRecipient{dispatcher("u1")}, stubScope{})
	ctx := context.Background()
	for _, id := range []string{"e1", "e2", "e3", "e4", "e5"} {
		_ = h.fanout.HandleAlert(ctx, alertEnvelope(id, "road_speeding", "high", "veh-1"))
	}
	_ = h.fanout.HandleAlert(ctx, alertEnvelope("e6", "geofence_exit", "high", "veh-1"))
	_ = h.fanout.HandleAlert(ctx, alertEnvelope("e7", "road_speeding", "high", "veh-2"))
	if len(h.provider.sent) != 3 {
		t.Fatalf("want one push per vehicle and alert type, got %d", len(h.provider.sent))
	}
}

func TestPushCapSendsOneDigestThenStaysSilent(t *testing.T) {
	h := newPushHarness([]PushRecipient{dispatcher("u1")}, stubScope{})
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		_ = h.fanout.HandleJobStatus(ctx, envelope("e"+string(rune('a'+i)), "job.status.changed", map[string]any{
			"job_id": "job-" + string(rune('a'+i)), "new_status": "assigned",
		}))
	}
	if len(h.provider.sent) != 9 {
		t.Fatalf("want 8 pushes and a digest, got %d", len(h.provider.sent))
	}
	digest := h.provider.sent[8].message
	if digest.Kind != domain.PushKindAlert || digest.ID != "" || digest.Body == "" {
		t.Fatalf("unexpected digest %+v", digest)
	}
}

func TestFailedPushIsRetriedOnRedelivery(t *testing.T) {
	h := newPushHarness([]PushRecipient{dispatcher("u1")}, stubScope{})
	event := alertEnvelope("e1", "road_speeding", "high", "veh-1")
	h.provider.err = errors.New("fcm 503")
	if err := h.fanout.HandleAlert(context.Background(), event); err == nil {
		t.Fatal("a failed push must be returned so JetStream redelivers")
	}
	h.provider.err = nil
	if err := h.fanout.HandleAlert(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(h.provider.sent) != 1 {
		t.Fatalf("the redelivery must not be coalesced away, got %d", len(h.provider.sent))
	}
}
