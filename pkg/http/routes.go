package httpx

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sort"

	"github.com/truckmoov/services-go/pkg/identity"
)

// Audience is who a route serves.
type Audience string

const (
	Admin   Audience = "admin"
	Tenant  Audience = "tenant"
	Service Audience = "service"
	Public  Audience = "public"
)

// Refusal is one request a route turned away, for the platform's IP blocking.
type Refusal struct {
	Method   string
	Path     string
	IP       string
	Audience string
	Reason   string
}

// RefusalReporter passes refusals on; it must not block the request.
type RefusalReporter func(Refusal)

// Routes registers every route together with who it serves. There is no way
// to register a route without saying so, so nothing is reachable by
// omission.
type Routes struct {
	mux      *http.ServeMux
	enforce  bool
	log      *slog.Logger
	report   RefusalReporter
	declared map[string][]Audience
}

// NewRoutes refuses for real only when enforce is set; otherwise it logs
// each request it would have refused and lets it through.
func NewRoutes(mux *http.ServeMux, enforce bool, log *slog.Logger, report RefusalReporter) *Routes {
	return &Routes{mux: mux, enforce: enforce, log: log, report: report, declared: map[string][]Audience{}}
}

// Handle panics on a route that names no audience: a route nobody may reach
// is a mistake to catch at start-up, not a refusal to discover in production.
func (r *Routes) Handle(pattern string, audiences []Audience, handler http.Handler) {
	if len(audiences) == 0 {
		panic("route " + pattern + " declares no audience")
	}
	r.declared[pattern] = audiences
	r.mux.Handle(pattern, r.guard(audiences, handler))
}

func (r *Routes) HandleFunc(pattern string, audiences []Audience, handler http.HandlerFunc) {
	r.Handle(pattern, audiences, handler)
}

// Declared lists every registered pattern, for the route inventory test.
func (r *Routes) Declared() []string {
	patterns := make([]string, 0, len(r.declared))
	for pattern := range r.declared {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	return patterns
}

func (r *Routes) guard(audiences []Audience, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		caller := CallerAudience(req)
		if admits(audiences, caller) {
			next.ServeHTTP(r.hidingForbidden(w, req, caller), req)
			return
		}
		// A token was presented and did not verify, most often because it
		// expired. 401 is what tells the portal to refresh it.
		if r.enforce && caller == Public && presentsCredentials(req) {
			writeUnauthorized(w, "missing or invalid authorization")
			return
		}
		r.refused(req, caller)
		if !r.enforce {
			next.ServeHTTP(w, req)
			return
		}
		// Exactly what the mux answers for a path it does not know: a
		// refusal must not tell the caller that something is here.
		http.NotFound(w, req)
	})
}

func (r *Routes) refused(req *http.Request, caller Audience) {
	refusal := Refusal{
		Method:   req.Method,
		Path:     req.URL.Path,
		IP:       clientIP(req),
		Audience: string(caller),
		Reason:   "audience_not_served",
	}
	if r.log != nil {
		r.log.Warn("route_refused", "method", refusal.Method, "path", refusal.Path, "ip", refusal.IP,
			"caller", refusal.Audience, "enforced", r.enforce)
	}
	// Only real refusals count toward IP blocking: while enforcement is off, a
	// gap in the route table would otherwise block legitimate users.
	if r.enforce && r.report != nil {
		r.report(refusal)
	}
}

func presentsCredentials(req *http.Request) bool {
	return req.Header.Get("Authorization") != "" || req.Header.Get(identity.ServiceTokenHeader) != ""
}

func admits(audiences []Audience, caller Audience) bool {
	for _, audience := range audiences {
		if audience == Public || audience == caller {
			return true
		}
	}
	return false
}

// CallerAudience is who is calling: a service, a user of one portal, or
// nobody the request proves.
func CallerAudience(req *http.Request) Audience {
	claims, ok := ClaimsFromRequest(req)
	switch {
	case !ok || claims == nil:
		return Public
	case claims.Service != "":
		return Service
	case claims.Portal == identity.PortalAdmin:
		return Admin
	default:
		return Tenant
	}
}

// hidingForbidden turns a handler's own 403 into the not-found answer, so a
// refusal deep inside a handler looks like a missing route too. 401 is left
// alone: it means the token needs refreshing, and the portals rely on it.
func (r *Routes) hidingForbidden(w http.ResponseWriter, req *http.Request, caller Audience) http.ResponseWriter {
	return &forbiddenHider{ResponseWriter: w, routes: r, req: req, caller: caller}
}

type forbiddenHider struct {
	http.ResponseWriter
	routes  *Routes
	req     *http.Request
	caller  Audience
	swallow bool
}

func (f *forbiddenHider) WriteHeader(status int) {
	if status != http.StatusForbidden {
		f.ResponseWriter.WriteHeader(status)
		return
	}
	f.routes.refused(f.req, f.caller)
	if !f.routes.enforce {
		f.ResponseWriter.WriteHeader(status)
		return
	}
	f.swallow = true
	f.ResponseWriter.Header().Del("Content-Type")
	http.NotFound(f.ResponseWriter, f.req)
}

func (f *forbiddenHider) Write(body []byte) (int, error) {
	if f.swallow {
		return len(body), nil
	}
	return f.ResponseWriter.Write(body)
}

func (f *forbiddenHider) Unwrap() http.ResponseWriter {
	return f.ResponseWriter
}

// Hijack keeps websocket upgrades working through the wrapper.
func (f *forbiddenHider) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := f.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response does not support hijacking")
	}
	return hijacker.Hijack()
}

func (f *forbiddenHider) Flush() {
	if flusher, ok := f.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
