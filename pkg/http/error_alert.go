package httpx

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/truckmoov/services-go/pkg/alert"
)

const (
	alertStackCapBytes = 4000
)

var (
	jwtPattern    = regexp.MustCompile(`[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	bearerPattern = regexp.MustCompile(`(?i)(bearer|authorization)\s*[:=]?\s*\S+`)
	emailPattern  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	idSegment     = regexp.MustCompile(`^[0-9a-fA-F-]{8,}$|^\d+$`)
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

// routeTemplate collapses UUID/numeric-looking path segments (tenant,
// vehicle, job IDs, etc.) into ":id" so alerts identify the route without
// leaking specific record identifiers to an external channel.
func routeTemplate(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if idSegment.MatchString(seg) {
			segments[i] = ":id"
		}
	}
	return strings.Join(segments, "/")
}

// statusRecorder wraps a ResponseWriter to capture the status code, so an
// error alert can report it. It deliberately does not capture the response
// body: handler bodies can echo request input (PII, validation messages
// quoting user-supplied strings, tenant data) and a denylist-style redactor
// can't be made complete for arbitrary handler output. Alerts carry the
// route/status/tenant only; the full body lives in the primary log store.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
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
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// Flush forwards to the underlying writer when it supports flushing, so
// streaming responses are not buffered by this wrapper.
func (s *statusRecorder) Flush() {
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack forwards to the underlying writer so WebSocket upgrades keep working
// when this recorder wraps the response (e.g. the tracking /tracking/ws route).
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := s.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("statusRecorder: underlying ResponseWriter does not implement http.Hijacker")
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
	route := routeTemplate(a.Path)

	// Prior requests are process-wide, not per-tenant: only surface entries
	// belonging to the same tenant as the failing request so one tenant's
	// alert never discloses another tenant's traffic (method/path/tenant).
	var samePrior []RequestSnapshot
	for _, p := range a.Prior {
		if p.TenantID == a.TenantID {
			p.Path = routeTemplate(p.Path)
			samePrior = append(samePrior, p)
		}
	}

	var body strings.Builder
	fmt.Fprintf(&body, "%s %s -> %d (tenant=%s)\n", a.Method, route, a.Status, tenant)
	if a.Detail != "" {
		fmt.Fprintf(&body, "\n%s\n", a.Detail)
	}
	if a.Stack != "" {
		fmt.Fprintf(&body, "\nstack:\n%s\n", a.Stack)
	}
	fmt.Fprintf(&body, "\nlast %d requests before this (same tenant):\n%s", len(samePrior), FormatRecentRequests(samePrior))

	title := fmt.Sprintf("%s: %d %s %s", serviceName, a.Status, a.Method, route)
	priority, tag := 3, "warning"
	if a.Status >= http.StatusInternalServerError || a.Stack != "" {
		priority, tag = 5, "rotating_light"
	}

	// Redact secret/PII-shaped substrings once, over the fully assembled
	// title and message, right before they leave the process for an
	// external channel.
	title = redact(title)
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
