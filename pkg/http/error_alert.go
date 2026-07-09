package httpx

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/truckmoov/services-go/pkg/alert"
)

const (
	alertBodyCapBytes  = 2048
	alertStackCapBytes = 4000
)

var (
	jwtPattern    = regexp.MustCompile(`[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	bearerPattern = regexp.MustCompile(`(?i)(bearer|authorization)\s*[:=]?\s*\S+`)
	emailPattern  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

// redact strips secret- and PII-shaped substrings before a payload leaves
// the process for an external alert channel: JWTs/access tokens, bearer/
// authorization headers echoed into error bodies, and email addresses.
func redact(s string) string {
	s = jwtPattern.ReplaceAllString(s, "[REDACTED_TOKEN]")
	s = bearerPattern.ReplaceAllString(s, "[REDACTED_AUTH]")
	s = emailPattern.ReplaceAllString(s, "[REDACTED_EMAIL]")
	return s
}

// statusRecorder wraps a ResponseWriter to capture the status code and a
// bounded prefix of the response body, so an error alert can show what the
// handler actually sent back.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        strings.Builder
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	if s.body.Len() < alertBodyCapBytes {
		remaining := alertBodyCapBytes - s.body.Len()
		if remaining > len(b) {
			remaining = len(b)
		}
		s.body.Write(b[:remaining])
	}
	return s.ResponseWriter.Write(b)
}

// WithErrorAlerts wraps next so that any non-2xx response or panic sends a
// push notification via notifier, including the response body (or panic
// stack trace) and the last few requests this process handled, for context
// on what the caller was doing beforehand. Must be wrapped inside
// WithAuthContext so tenant claims are already on the request context.
func WithErrorAlerts(next http.Handler, notifier *alert.NtfyClient, recent *RecentRequests, serviceName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prior := recent.Snapshot()
		rec := &statusRecorder{ResponseWriter: w}

		defer func() {
			if rerr := recover(); rerr != nil {
				stack := debug.Stack()
				sendAlert(notifier, serviceName, alertContext{
					Method:   r.Method,
					Path:     r.URL.Path,
					Status:   http.StatusInternalServerError,
					TenantID: TenantIDFromRequest(r),
					Detail:   fmt.Sprintf("panic: %v", rerr),
					Stack:    truncate(string(stack), alertStackCapBytes),
					Prior:    prior,
				})
				recent.Add(RequestSnapshot{
					Method: r.Method, Path: r.URL.Path, Status: http.StatusInternalServerError,
					TenantID: TenantIDFromRequest(r), At: time.Now(),
				})

				if !rec.wroteHeader {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				}
			}
		}()

		next.ServeHTTP(rec, r)

		tenantID := TenantIDFromRequest(r)
		if rec.status >= http.StatusBadRequest {
			sendAlert(notifier, serviceName, alertContext{
				Method:   r.Method,
				Path:     r.URL.Path,
				Status:   rec.status,
				TenantID: tenantID,
				Detail:   rec.body.String(),
				Prior:    prior,
			})
		}
		recent.Add(RequestSnapshot{
			Method: r.Method, Path: r.URL.Path, Status: rec.status, TenantID: tenantID, At: time.Now(),
		})
	})
}

type alertContext struct {
	Method   string
	Path     string
	Status   int
	TenantID string
	Detail   string
	Stack    string
	Prior    []RequestSnapshot
}

func sendAlert(notifier *alert.NtfyClient, serviceName string, a alertContext) {
	tenant := a.TenantID
	if tenant == "" {
		tenant = "-"
	}

	var body strings.Builder
	fmt.Fprintf(&body, "%s %s -> %d (tenant=%s)\n", a.Method, a.Path, a.Status, tenant)
	if a.Detail != "" {
		fmt.Fprintf(&body, "\n%s\n", a.Detail)
	}
	if a.Stack != "" {
		fmt.Fprintf(&body, "\nstack:\n%s\n", a.Stack)
	}
	fmt.Fprintf(&body, "\nlast %d requests before this:\n%s", len(a.Prior), FormatRecentRequests(a.Prior))

	title := fmt.Sprintf("%s: %d %s %s", serviceName, a.Status, a.Method, a.Path)
	priority, tag := 3, "warning"
	if a.Status >= http.StatusInternalServerError || a.Stack != "" {
		priority, tag = 5, "rotating_light"
	}

	// Redact secret/PII-shaped substrings once, over the fully assembled
	// message, right before it leaves the process for an external channel.
	message := redact(body.String())

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = notifier.Send(ctx, title, message, priority, tag)
	}()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...(truncated)"
}
