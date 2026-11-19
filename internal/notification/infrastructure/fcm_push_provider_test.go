package infrastructure

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type memoryPushDevices struct {
	mu      sync.Mutex
	tokens  []string
	deleted []string
}

func (m *memoryPushDevices) TokensFor(context.Context, string, string) ([]string, error) {
	return m.tokens, nil
}

func (m *memoryPushDevices) DeleteToken(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, token)
	return nil
}

type memoryClaims struct{ held map[string]bool }

func (m *memoryClaims) Claim(_ context.Context, key string, _ time.Duration) (bool, error) {
	if m.held[key] {
		return false, nil
	}
	m.held[key] = true
	return true, nil
}

func (m *memoryClaims) Release(_ context.Context, key string) error {
	delete(m.held, key)
	return nil
}

type fakeFCM struct {
	tokenCalls int
	messages   []map[string]any
	reply      func(token string) (int, string)
	server     *httptest.Server
}

func newFakeFCM(t *testing.T) *fakeFCM {
	f := &fakeFCM{reply: func(string) (int, string) { return http.StatusOK, `{"name":"projects/p/messages/1"}` }}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			parts := strings.Split(r.PostForm.Get("assertion"), ".")
			claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var decoded map[string]any
			_ = json.Unmarshal(claims, &decoded)
			if len(parts) != 3 || decoded["iss"] != "push@fonudev.iam" || decoded["scope"] != fcmScope {
				t.Errorf("bad assertion claims %v", decoded)
			}
			f.tokenCalls++
			_, _ = w.Write([]byte(`{"access_token":"ya29.token","expires_in":3600}`))
		case r.URL.Path == "/v1/projects/fonudev/messages:send":
			if r.Header.Get("Authorization") != "Bearer ya29.token" {
				t.Errorf("missing access token")
			}
			var body struct {
				Message map[string]any `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.messages = append(f.messages, body.Message)
			status, reply := f.reply(body.Message["token"].(string))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(reply))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func newTestFCMProvider(t *testing.T, f *fakeFCM, devices *memoryPushDevices) *FCMPushProvider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	account, _ := json.Marshal(map[string]string{
		"client_email": "push@fonudev.iam",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":    f.server.URL + "/token",
	})
	provider, err := NewFCMPushProvider(f.server.URL, "fonudev", string(account), devices, &memoryClaims{held: map[string]bool{}}, f.server.Client())
	if err != nil || provider == nil {
		t.Fatalf("provider: %v", err)
	}
	return provider
}

func pushContent() string {
	content, _ := json.Marshal(domain.PushMessage{
		Title: "Job assigned", Body: "A job is now assigned.", Kind: "job", ID: "job-1", CollapseKey: "job:job-1", Category: "jobs",
		DeliveryID: "push_evt-1_user-1",
	})
	return string(content)
}

func TestFCMProviderIsOffWithoutConfig(t *testing.T) {
	provider, err := NewFCMPushProvider("", "", "", &memoryPushDevices{}, nil, nil)
	if provider != nil || err != nil {
		t.Fatalf("missing config must mean no provider, not an error: %v", err)
	}
}

func TestFCMProviderSendsTheContractPayloadToEveryDevice(t *testing.T) {
	f := newFakeFCM(t)
	provider := newTestFCMProvider(t, f, &memoryPushDevices{tokens: []string{"tok-ios", "tok-android"}})

	status, err := provider.Send(context.Background(), "tenant-a", "push", "user-1", pushContent())
	if err != nil || status != domain.StatusSent || len(f.messages) != 2 {
		t.Fatalf("status=%s err=%v messages=%d", status, err, len(f.messages))
	}
	message := f.messages[0]
	data := message["data"].(map[string]any)
	notification := message["notification"].(map[string]any)
	android := message["android"].(map[string]any)
	apns := message["apns"].(map[string]any)
	headers := apns["headers"].(map[string]any)
	aps := apns["payload"].(map[string]any)["aps"].(map[string]any)
	if data["kind"] != "job" || data["id"] != "job-1" || len(data) != 2 ||
		notification["title"] != "Job assigned" || notification["body"] == "" ||
		android["collapse_key"] != "job:job-1" || android["priority"] != "HIGH" ||
		headers["apns-collapse-id"] != "job:job-1" || headers["apns-priority"] != "10" || aps["thread-id"] != "jobs" {
		t.Fatalf("payload breaks the contract: %v", message)
	}
}

func TestFCMProviderCachesTheAccessTokenUntilNearExpiry(t *testing.T) {
	f := newFakeFCM(t)
	provider := newTestFCMProvider(t, f, &memoryPushDevices{tokens: []string{"tok"}})
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	provider.now = func() time.Time { return now }

	_, _ = provider.Send(context.Background(), "t", "push", "u", pushContent())
	now = now.Add(50 * time.Minute)
	_, _ = provider.Send(context.Background(), "t", "push", "u", pushContent())
	if f.tokenCalls != 1 {
		t.Fatalf("want one token fetch while it is fresh, got %d", f.tokenCalls)
	}
	now = now.Add(6 * time.Minute)
	_, _ = provider.Send(context.Background(), "t", "push", "u", pushContent())
	if f.tokenCalls != 2 {
		t.Fatalf("want a refresh within five minutes of expiry, got %d", f.tokenCalls)
	}
}

func TestFCMProviderDeletesDeadTokens(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"unregistered":          {404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`},
		"bare 404":              {404, `{}`},
		"unregistered 400":      {400, `{"error":{"status":"INVALID_ARGUMENT","details":[{"errorCode":"UNREGISTERED"}]}}`},
		"invalid token":         {400, `{"error":{"status":"INVALID_ARGUMENT","message":"The registration token is not a valid FCM registration token"}}`},
		"apns topic disallowed": {400, `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"},{"@type":"type.googleapis.com/google.firebase.fcm.v1.ApnsError","statusCode":400,"reason":"TopicDisallowed"}]}}`},
		"apns bad token":        {400, `{"error":{"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.ApnsError","statusCode":400,"reason":"BadDeviceToken"}]}}`},
		"apns wrong topic":      {400, `{"error":{"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.ApnsError","statusCode":400,"reason":"DeviceTokenNotForTopic"}]}}`},
		"apns unregistered":     {410, `{"error":{"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.ApnsError","statusCode":410,"reason":"Unregistered"}]}}`},
		"sender id mismatch":    {403, `{"error":{"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"SENDER_ID_MISMATCH"}]}}`},
	}
	for name, tc := range cases {
		f := newFakeFCM(t)
		f.reply = func(string) (int, string) { return tc.status, tc.body }
		devices := &memoryPushDevices{tokens: []string{"dead"}}
		provider := newTestFCMProvider(t, f, devices)
		if _, err := provider.Send(context.Background(), "t", "push", "u", pushContent()); err != nil {
			t.Fatalf("%s: a dead token is cleaned up, not retried: %v", name, err)
		}
		if len(devices.deleted) != 1 || devices.deleted[0] != "dead" {
			t.Fatalf("%s: want the token deleted, got %v", name, devices.deleted)
		}
	}
}

func TestFCMProviderRetriesThrottlingAndOutages(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{429, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`},
		{503, `{"error":{"status":"UNAVAILABLE"}}`},
		{401, `{"error":{"status":"UNAUTHENTICATED"}}`},
	} {
		f := newFakeFCM(t)
		f.reply = func(string) (int, string) { return tc.status, tc.body }
		devices := &memoryPushDevices{tokens: []string{"live"}}
		provider := newTestFCMProvider(t, f, devices)
		if _, err := provider.Send(context.Background(), "t", "push", "u", pushContent()); err == nil {
			t.Fatalf("%d must return an error so the event is redelivered", tc.status)
		}
		if len(devices.deleted) != 0 {
			t.Fatalf("%d must not delete the device", tc.status)
		}
	}
}

func TestCollapseIDFitsAPNsLimit(t *testing.T) {
	long := "alert:" + strings.Repeat("v", 80) + ":road_speeding"
	if got := collapseID(long); len(got) > 64 || got != collapseID(long) {
		t.Fatalf("collapse id %q", got)
	}
}

func TestFCMProviderRetriesOnlyTheDevicesThatFailed(t *testing.T) {
	f := newFakeFCM(t)
	failing := true
	f.reply = func(token string) (int, string) {
		if token == "tok-b" && failing {
			return 503, `{"error":{"status":"UNAVAILABLE"}}`
		}
		return 200, `{"name":"projects/p/messages/1"}`
	}
	provider := newTestFCMProvider(t, f, &memoryPushDevices{tokens: []string{"tok-a", "tok-b", "tok-c"}})

	if _, err := provider.Send(context.Background(), "t", "push", "u", pushContent()); err == nil {
		t.Fatal("a transient failure on one device must ask for a redelivery")
	}
	failing = false
	if status, err := provider.Send(context.Background(), "t", "push", "u", pushContent()); err != nil || status != domain.StatusSent {
		t.Fatalf("redelivery: %s %v", status, err)
	}
	sent := map[string]int{}
	for _, message := range f.messages {
		sent[message["token"].(string)]++
	}
	if sent["tok-a"] != 1 || sent["tok-c"] != 1 || sent["tok-b"] != 2 {
		t.Fatalf("delivered devices must not be pushed again: %v", sent)
	}
}

func TestFCMProviderDoesNotFailTheSendForOneRejectedDevice(t *testing.T) {
	f := newFakeFCM(t)
	f.reply = func(token string) (int, string) {
		if token == "tok-bad" {
			return 400, `{"error":{"status":"INVALID_ARGUMENT","message":"Invalid JSON payload"}}`
		}
		return 200, `{}`
	}
	devices := &memoryPushDevices{tokens: []string{"tok-bad", "tok-ok"}}
	provider := newTestFCMProvider(t, f, devices)
	if status, err := provider.Send(context.Background(), "t", "push", "u", pushContent()); err != nil || status != domain.StatusSent {
		t.Fatalf("one rejected device must not fail the send: %s %v", status, err)
	}
	if len(devices.deleted) != 0 {
		t.Fatal("a malformed message is not a dead token")
	}

	devices.tokens = []string{"tok-bad"}
	content := strings.Replace(pushContent(), "push_evt-1_user-1", "push_evt-2_user-1", 1)
	if status, err := provider.Send(context.Background(), "t", "push", "u", content); err != nil || status != domain.StatusFailed {
		t.Fatalf("every device rejecting is a failed send that is not retried: %s %v", status, err)
	}
}
