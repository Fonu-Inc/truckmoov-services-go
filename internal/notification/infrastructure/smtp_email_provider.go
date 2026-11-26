package infrastructure

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/truckmoov/services-go/internal/notification/domain"
)

// SMTPSender is the seam the tests replace. The real implementation dials the
// configured relay; a test supplies one that records what it was asked to send.
type SMTPSender interface {
	Send(ctx context.Context, from string, to []string, message []byte) error
}

type SMTPEmailMessageProvider struct {
	host        string
	port        int
	username    string
	password    string
	fromAddress string
	fromName    string
	sender      SMTPSender
}

func NewSMTPEmailMessageProvider(host string, port int, username, password, fromAddress, fromName string) *SMTPEmailMessageProvider {
	return NewSMTPEmailMessageProviderWithSender(
		host, port, username, password, fromAddress, fromName,
		&netSMTPSender{
			host:     strings.TrimSpace(host),
			port:     port,
			username: strings.TrimSpace(username),
			password: strings.TrimSpace(password),
		},
	)
}

func NewSMTPEmailMessageProviderWithSender(host string, port int, username, password, fromAddress, fromName string, sender SMTPSender) *SMTPEmailMessageProvider {
	from := strings.TrimSpace(fromAddress)
	if from == "" {
		from = strings.TrimSpace(username)
	}
	name := strings.TrimSpace(fromName)
	if name == "" {
		name = "FleetPro"
	}
	return &SMTPEmailMessageProvider{
		host:        strings.TrimSpace(host),
		port:        port,
		username:    strings.TrimSpace(username),
		password:    strings.TrimSpace(password),
		fromAddress: from,
		fromName:    name,
		sender:      sender,
	}
}

func (p *SMTPEmailMessageProvider) Send(ctx context.Context, _ string, channel, recipient, content string) (string, error) {
	trimmedChannel := strings.TrimSpace(channel)
	if trimmedChannel != "" && !strings.EqualFold(trimmedChannel, "email") {
		return domain.StatusFailed, fmt.Errorf("unsupported notification channel: %s", channel)
	}
	if err := p.validateConfiguration(); err != nil {
		return domain.StatusFailed, err
	}

	address, err := mail.ParseAddress(strings.TrimSpace(recipient))
	if err != nil {
		return domain.StatusFailed, fmt.Errorf("invalid email recipient: %w", err)
	}
	subject, body := splitSubjectAndBody(content)
	if strings.TrimSpace(body) == "" {
		return domain.StatusFailed, errors.New("content is required")
	}

	message := p.buildMessage(address.Address, subject, body)
	if err := p.sender.Send(ctx, p.fromAddress, []string{address.Address}, message); err != nil {
		return domain.StatusFailed, fmt.Errorf("send email: %w", err)
	}
	return domain.StatusSent, nil
}

func (p *SMTPEmailMessageProvider) validateConfiguration() error {
	if p.host == "" || p.port == 0 || p.fromAddress == "" {
		return errors.New("email provider is not configured; set SMTP_HOST, SMTP_PORT, and SMTP_FROM_ADDRESS")
	}
	return nil
}

func (p *SMTPEmailMessageProvider) buildMessage(recipient, subject, body string) []byte {
	from := (&mail.Address{Name: p.fromName, Address: p.fromAddress}).String()
	headers := []string{
		"From: " + from,
		"To: " + recipient,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=\"utf-8\"",
		"Content-Transfer-Encoding: 8bit",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + normalizeLineEndings(body) + "\r\n")
}

// splitSubjectAndBody lets a caller put the subject on the first line followed
// by a blank line, which keeps the generic {recipient, content, channel} event
// shape usable for email without inventing a second event contract.
func splitSubjectAndBody(content string) (string, string) {
	normalized := strings.ReplaceAll(strings.TrimSpace(content), "\r\n", "\n")
	if subject, body, found := strings.Cut(normalized, "\n\n"); found {
		trimmedSubject := strings.TrimSpace(subject)
		if trimmedSubject != "" && !strings.Contains(trimmedSubject, "\n") {
			return trimmedSubject, strings.TrimSpace(body)
		}
	}
	return "FleetPro notification", normalized
}

func normalizeLineEndings(body string) string {
	return strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
}

// implicitTLSPort is the SMTPS port, where the connection is wrapped in TLS
// from the first byte rather than upgraded through STARTTLS. The auth service's
// mailer decides the same way (nodemailer's `secure: port === 465`), so both
// services behave identically against one shared SMTP configuration.
const implicitTLSPort = 465

type netSMTPSender struct {
	host     string
	port     int
	username string
	password string
}

func (s *netSMTPSender) Send(ctx context.Context, from string, to []string, message []byte) error {
	address := net.JoinHostPort(s.host, fmt.Sprintf("%d", s.port))

	tlsConfig := &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	var conn net.Conn
	var err error
	if s.port == implicitTLSPort {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("dial smtp %s: %w", address, err)
	}

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if s.port != implicitTLSPort {
		if ok, _ := client.Extension("STARTTLS"); ok {
			// ServerName is set so the relay's certificate is verified against
			// the host we meant to reach, rather than skipped.
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}

	if s.username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt to: %w", err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		writer.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}

	return client.Quit()
}
