package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
)

type vectorCase struct {
	Name           string        `json:"name"`
	Subject        string        `json:"subject"`
	Envelope       EventEnvelope `json:"envelope"`
	CanonicalData  string        `json:"canonical_data"`
	CanonicalActor string        `json:"canonical_actor"`
	Sig            string        `json:"sig"`
	Unsupported    bool          `json:"unsupported"`
}

type vectorFile struct {
	Secret string       `json:"secret"`
	Cases  []vectorCase `json:"cases"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/bus_signature_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSharedVectors(t *testing.T) {
	f := loadVectors(t)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Unsupported {
				if _, err := BusSignature(f.Secret, c.Subject, c.Envelope); err == nil {
					t.Fatal("expected unsupported value to fail")
				}
				return
			}
			data, err := CanonicalJSON(objectOrEmpty(c.Envelope.Data))
			if err != nil || string(data) != c.CanonicalData {
				t.Fatalf("data %s err %v want %s", data, err, c.CanonicalData)
			}
			actor, err := CanonicalJSON(objectOrEmpty(c.Envelope.Actor))
			if err != nil || string(actor) != c.CanonicalActor {
				t.Fatalf("actor %s err %v want %s", actor, err, c.CanonicalActor)
			}
			sig, err := BusSignature(f.Secret, c.Subject, c.Envelope)
			if err != nil || sig != c.Sig {
				t.Fatalf("sig %s err %v want %s", sig, err, c.Sig)
			}
		})
	}
}

func TestCanonicalJSONTypedValuesMatchWire(t *testing.T) {
	typed := map[string]any{"ids": []string{"b", "a"}, "n": 3, "big": int64(7), "nested": map[string]string{"k": "v"}}
	got, err := CanonicalJSON(typed)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"big":7,"ids":["b","a"],"n":3,"nested":{"k":"v"}}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestCanonicalJSONRejectsUnsupported(t *testing.T) {
	for _, bad := range []any{1e-7, float64(1 << 53), 1e21, int64(1) << 60} {
		if _, err := CanonicalJSON(map[string]any{"v": bad}); err == nil {
			t.Errorf("expected %v to be unsupported", bad)
		}
	}
}

const (
	testSecret  = "bus-secret"
	testSubject = "tm.acme.tenant.suspended"
)

func signedEnvelope(t *testing.T) EventEnvelope {
	t.Helper()
	e := EventEnvelope{
		ID: "evt-1", Type: "tenant.suspended", TenantID: "acme", Subject: testSubject,
		Data: map[string]any{"reason": "billing", "count": float64(2)}, Actor: map[string]any{"user_id": "u1"},
	}
	sig, err := BusSignature(testSecret, testSubject, e)
	if err != nil {
		t.Fatal(err)
	}
	e.BusSignature = sig
	return e
}

func TestVerifyBus(t *testing.T) {
	tamper := func(mutate func(*EventEnvelope)) EventEnvelope {
		e := signedEnvelope(t)
		mutate(&e)
		return e
	}
	cases := []struct {
		name   string
		secret string
		env    EventEnvelope
		opts   VerifyOptions
		want   error
	}{
		{"signed", testSecret, signedEnvelope(t), VerifyOptions{}, nil},
		{"secret unset", "", signedEnvelope(t), VerifyOptions{AcceptUnsigned: true}, ErrSecretUnset},
		{"wrong secret", "other", signedEnvelope(t), VerifyOptions{}, ErrBadSignature},
		{"tampered data", testSecret, tamper(func(e *EventEnvelope) { e.Data["reason"] = "x" }), VerifyOptions{}, ErrBadSignature},
		{"tampered actor", testSecret, tamper(func(e *EventEnvelope) { e.Actor = map[string]any{"user_id": "u2"} }), VerifyOptions{}, ErrBadSignature},
		{"tampered id", testSecret, tamper(func(e *EventEnvelope) { e.ID = "evt-2" }), VerifyOptions{}, ErrBadSignature},
		{"tampered type", testSecret, tamper(func(e *EventEnvelope) { e.Type = "tenant.reinstated" }), VerifyOptions{}, ErrBadSignature},
		{"replayed on other subject", testSecret, tamper(func(e *EventEnvelope) { e.Subject = "tm.acme.tenant.reinstated" }), VerifyOptions{}, ErrBadSignature},
		{"wrong tenant", testSecret, tamper(func(e *EventEnvelope) { e.Subject = "tm.other.tenant.suspended" }), VerifyOptions{}, ErrTenantMismatch},
		{"short subject", testSecret, tamper(func(e *EventEnvelope) { e.Subject = "tm.acme" }), VerifyOptions{}, ErrBadSubject},
		{"empty subject", testSecret, tamper(func(e *EventEnvelope) { e.Subject = "" }), VerifyOptions{}, ErrBadSubject},
		{"unsigned", testSecret, tamper(func(e *EventEnvelope) { e.BusSignature = "" }), VerifyOptions{}, ErrUnsigned},
		{"unsigned with flag", testSecret, tamper(func(e *EventEnvelope) { e.BusSignature = "" }), VerifyOptions{AcceptUnsigned: true}, nil},
		{"wrong sig with flag", testSecret, tamper(func(e *EventEnvelope) { e.BusSignature = "00" }), VerifyOptions{AcceptUnsigned: true}, ErrBadSignature},
		{"notification sig is not a bus sig", testSecret, tamper(func(e *EventEnvelope) { e.BusSignature = ""; e.Signature = "abc" }), VerifyOptions{}, ErrUnsigned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := VerifyBus(c.secret, c.env, c.opts); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
}

func TestVerifiedAcksRejectedEvents(t *testing.T) {
	calls := 0
	handler := func(context.Context, EventEnvelope) error { calls++; return errors.New("boom") }
	wrapped := Verified(handler, testSecret, VerifyOptions{}, quietLog())

	forged := signedEnvelope(t)
	forged.BusSignature = ""
	if err := wrapped(context.Background(), forged); err != nil || calls != 0 {
		t.Fatalf("forged: err %v calls %d", err, calls)
	}
	if err := wrapped(context.Background(), signedEnvelope(t)); err == nil || calls != 1 {
		t.Fatalf("signed must reach handler and surface its error: err %v calls %d", err, calls)
	}
}

func TestVerifiedWithoutSecretRejectsEverything(t *testing.T) {
	calls := 0
	wrapped := Verified(func(context.Context, EventEnvelope) error { calls++; return nil }, "", VerifyOptions{AcceptUnsigned: true}, quietLog())
	if err := wrapped(context.Background(), signedEnvelope(t)); err != nil || calls != 0 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestPublishSigning(t *testing.T) {
	signing := &natsEventBus{log: quietLog(), signingSecret: testSecret}
	e := signedEnvelope(t)
	e.BusSignature = "forged"
	out := signing.sign(testSubject, e)
	out.Subject = testSubject
	if err := VerifyBus(testSecret, out, VerifyOptions{}); err != nil {
		t.Fatalf("published envelope must verify: %v", err)
	}

	unsignable := signedEnvelope(t)
	unsignable.Data = map[string]any{"v": 1e-7}
	if got := signing.sign(testSubject, unsignable); got.BusSignature != "" {
		t.Fatal("unsignable event must go out unsigned")
	}

	unconfigured := &natsEventBus{log: quietLog()}
	if got := unconfigured.sign(testSubject, signedEnvelope(t)); got.BusSignature != "" {
		t.Fatal("no secret: caller bsig must be stripped")
	}
}
