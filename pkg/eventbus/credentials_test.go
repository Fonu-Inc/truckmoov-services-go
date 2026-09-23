package eventbus

import (
	"testing"

	"github.com/nats-io/nats.go"
)

func applied(creds Credentials) nats.Options {
	opts := nats.GetDefaultOptions()
	for _, option := range creds.options() {
		_ = option(&opts)
	}
	return opts
}

func TestCredentialsPresentTheServiceLogin(t *testing.T) {
	opts := applied(Credentials{User: "tracking", Password: "secret"})

	if opts.User != "tracking" || opts.Password != "secret" {
		t.Fatalf("expected the service login, got %q/%q", opts.User, opts.Password)
	}
}

func TestEmptyCredentialsConnectAnonymously(t *testing.T) {
	opts := applied(Credentials{})

	if opts.User != "" || opts.Password != "" {
		t.Fatalf("expected no login, got %q/%q", opts.User, opts.Password)
	}
	if opts.Name != "truckmoov-eventbus" {
		t.Fatalf("expected the connection name kept, got %q", opts.Name)
	}
}
