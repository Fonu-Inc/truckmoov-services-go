package infrastructure

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

type recordingSMTPSender struct {
	calls   int
	from    string
	to      []string
	message string
	err     error
}

func (s *recordingSMTPSender) Send(_ context.Context, from string, to []string, message []byte) error {
	s.calls++
	s.from = from
	s.to = to
	s.message = string(message)
	return s.err
}

func newProvider(sender SMTPSender) *SMTPEmailMessageProvider {
	return NewSMTPEmailMessageProviderWithSender(
		"smtp.example.com", 587, "mailer@example.com", "secret",
		"no-reply@truckmoov.com", "TruckMoov", sender,
	)
}

func TestSMTPEmailProviderSendsToRecipient(t *testing.T) {
	sender := &recordingSMTPSender{}
	provider := newProvider(sender)

	status, err := provider.Send(context.Background(), "tenant-a", "email", "ops@carrier.ng", "Licence expiring\n\nTwo documents expire this week.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != domain.StatusSent {
		t.Fatalf("expected status %q, got %q", domain.StatusSent, status)
	}
	if sender.calls != 1 {
		t.Fatalf("expected 1 send, got %d", sender.calls)
	}
	if sender.from != "no-reply@truckmoov.com" {
		t.Fatalf("unexpected from address: %s", sender.from)
	}
	if len(sender.to) != 1 || sender.to[0] != "ops@carrier.ng" {
		t.Fatalf("unexpected recipients: %v", sender.to)
	}
}

func TestSMTPEmailProviderUsesFirstLineAsSubject(t *testing.T) {
	sender := &recordingSMTPSender{}
	provider := newProvider(sender)

	if _, err := provider.Send(context.Background(), "tenant-a", "email", "ops@carrier.ng", "Licence expiring\n\nBody text here."); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sender.message, "Subject: Licence expiring") {
		t.Fatalf("subject header missing from message:\n%s", sender.message)
	}
	if !strings.Contains(sender.message, "Body text here.") {
		t.Fatalf("body missing from message:\n%s", sender.message)
	}
}

func TestSMTPEmailProviderFallsBackToDefaultSubject(t *testing.T) {
	sender := &recordingSMTPSender{}
	provider := newProvider(sender)

	if _, err := provider.Send(context.Background(), "tenant-a", "email", "ops@carrier.ng", "Single line only"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sender.message, "Subject: FleetPro notification") {
		t.Fatalf("expected default subject:\n%s", sender.message)
	}
}

func TestSMTPEmailProviderSetsHeadersAndCRLF(t *testing.T) {
	sender := &recordingSMTPSender{}
	provider := newProvider(sender)

	if _, err := provider.Send(context.Background(), "tenant-a", "email", "ops@carrier.ng", "Subject line\n\nLine one\nLine two"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, header := range []string{"From: ", "To: ", "MIME-Version: 1.0", "Content-Type: text/plain"} {
		if !strings.Contains(sender.message, header) {
			t.Fatalf("missing header %q in:\n%s", header, sender.message)
		}
	}
	if !strings.Contains(sender.message, "Line one\r\nLine two") {
		t.Fatalf("body lines should use CRLF:\n%q", sender.message)
	}
}

func TestSMTPEmailProviderRejectsInvalidRecipient(t *testing.T) {
	sender := &recordingSMTPSender{}
	provider := newProvider(sender)

	status, err := provider.Send(context.Background(), "tenant-a", "email", "not-an-address", "Subject\n\nBody")
	if err == nil {
		t.Fatal("expected an error for an invalid recipient")
	}
	if status != domain.StatusFailed {
		t.Fatalf("expected status %q, got %q", domain.StatusFailed, status)
	}
	if sender.calls != 0 {
		t.Fatalf("expected no send attempt, got %d", sender.calls)
	}
}

func TestSMTPEmailProviderRejectsOtherChannels(t *testing.T) {
	sender := &recordingSMTPSender{}
	provider := newProvider(sender)

	if _, err := provider.Send(context.Background(), "tenant-a", "sms", "ops@carrier.ng", "Subject\n\nBody"); err == nil {
		t.Fatal("expected an error for a non-email channel")
	}
	if sender.calls != 0 {
		t.Fatalf("expected no send attempt, got %d", sender.calls)
	}
}

func TestSMTPEmailProviderReportsMissingConfiguration(t *testing.T) {
	provider := NewSMTPEmailMessageProviderWithSender("", 0, "", "", "", "", &recordingSMTPSender{})

	status, err := provider.Send(context.Background(), "tenant-a", "email", "ops@carrier.ng", "Subject\n\nBody")
	if err == nil {
		t.Fatal("expected a configuration error")
	}
	if status != domain.StatusFailed {
		t.Fatalf("expected status %q, got %q", domain.StatusFailed, status)
	}
}

func TestSMTPEmailProviderPropagatesSendFailure(t *testing.T) {
	sender := &recordingSMTPSender{err: errors.New("relay refused")}
	provider := newProvider(sender)

	status, err := provider.Send(context.Background(), "tenant-a", "email", "ops@carrier.ng", "Subject\n\nBody")
	if err == nil || !strings.Contains(err.Error(), "relay refused") {
		t.Fatalf("expected the relay error to surface, got %v", err)
	}
	if status != domain.StatusFailed {
		t.Fatalf("expected status %q, got %q", domain.StatusFailed, status)
	}
}

func TestMultiChannelProviderRoutesEmail(t *testing.T) {
	sender := &recordingSMTPSender{}
	provider := NewMultiChannelMessageProvider(nil, nil, newProvider(sender))

	status, err := provider.Send(context.Background(), "tenant-a", "email", "ops@carrier.ng", "Subject\n\nBody")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != domain.StatusSent {
		t.Fatalf("expected status %q, got %q", domain.StatusSent, status)
	}
	if sender.calls != 1 {
		t.Fatalf("expected the email provider to be used, calls=%d", sender.calls)
	}
}

func TestMultiChannelProviderReportsUnconfiguredEmail(t *testing.T) {
	provider := NewMultiChannelMessageProvider(nil, nil, nil)

	if _, err := provider.Send(context.Background(), "tenant-a", "email", "ops@carrier.ng", "Subject\n\nBody"); err == nil {
		t.Fatal("expected an error when the email provider is absent")
	}
}
