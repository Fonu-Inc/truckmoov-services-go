package httpx

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sort"

	"github.com/truckmoov/services-go/pkg/identity"
)

// Audience is who a route serves.
type Audience string

const (
	Admin     Audience = "admin"
	Tenant    Audience = "tenant"
	Installer Audience = "installer"
	Service   Audience = "service"
	Public    Audience = "public"
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
	mux     *http.ServeMux
	enforce bool
	// enforcePermissions refuses users lacking a route's permission; off,
	// such requests are only logged.
	enforcePermissions bool
	log                *slog.Logger
	report             RefusalReporter
	declared           map[string][]Audience
	needs              map[string]Requirement
}

// NewRoutes refuses for real only when enforce is set; otherwise it logs
// each request it would have refused and lets it through.
func NewRoutes(mux *http.ServeMux, enforce bool, log *slog.Logger, report RefusalReporter) *Routes {
	// An unknown path answers with the same JSON 404 a refusal does, so the
	// two stay indistinguishable and the contract holds for both.
	mux.HandleFunc("/", NotFound)
	return &Routes{mux: mux, enforce: enforce, log: log, report: report, declared: map[string][]Audience{}, needs: map[string]Requirement{}}
}

// EnforcePermissions switches permission refusals on.
func (r *Routes) EnforcePermissions(on bool) *Routes {
	r.enforcePermissions = on
	return r
}

// Handle panics on a route that names no audience, or a route users reach
// that names no permission: a route nobody may reach is a mistake to catch
// at start-up, not a refusal to discover in production.
func (r *Routes) Handle(pattern string, audiences []Audience, need Requirement, handler http.Handler) {
	if len(audiences) == 0 {
		panic("route " + pattern + " declares no audience")
	}
	if servesUsers(audiences) && !need.declared() {
		panic("route " + pattern + " declares no permission")
	}
	r.declared[pattern] = audiences
	r.needs[pattern] = need
	r.mux.Handle(pattern, r.guard(audiences, need, handler))
}

func (r *Routes) HandleFunc(pattern string, audiences []Audience, need Requirement, handler http.HandlerFunc) {
	r.Handle(pattern, audiences, need, handler)
}

// Permissions lists every permission any route needs, for the tests that
// check each one is a real catalog permission some role can hold.
func (r *Routes) Permissions() []string {
	var all []string
	for _, need := range r.needs {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			all = append(all, need.needed(method)...)
		}
	}
	sort.Strings(all)
	return slices.Compact(all)
}

func servesUsers(audiences []Audience) bool {
	return slices.Contains(audiences, Tenant) || slices.Contains(audiences, Admin) || slices.Contains(audiences, Installer)
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

func (r *Routes) guard(audiences []Audience, need Requirement, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		caller := CallerAudience(req)
		if admits(audiences, caller) {
			if r.lacksPermission(req, caller, audiences, need) {
				r.refused(req, caller, "missing_permission", r.enforcePermissions)
				if r.enforcePermissions {
					NotFound(w, req)
					return
				}
			}
			next.ServeHTTP(r.hidingForbidden(w, req, caller), req)
			return
		}
		// A token was presented and did not verify, most often because it
		// expired. 401 is what tells the portal to refresh it.
		if r.enforce && caller == Public && presentsCredentials(req) {
			writeUnauthorized(w, "missing or invalid authorization")
			return
		}
		r.refused(req, caller, "audience_not_served", r.enforce)
		if !r.enforce {
			next.ServeHTTP(w, req)
			return
		}
		// Exactly what the mux answers for a path it does not know: a
		// refusal must not tell the caller that something is here.
		NotFound(w, req)
	})
}

func (r *Routes) refused(req *http.Request, caller Audience, reason string, enforced bool) {
	refusal := Refusal{
		Method:   req.Method,
		Path:     req.URL.Path,
		IP:       clientIP(req),
		Audience: string(caller),
		Reason:   reason,
	}
	if r.log != nil {
		r.log.Warn("route_refused", "method", refusal.Method, "path", refusal.Path, "ip", refusal.IP,
			"caller", refusal.Audience, "reason", reason, "enforced", enforced)
	}
	// Only real refusals count toward IP blocking: while enforcement is off, a
	// gap in the route table would otherwise block legitimate users.
	if enforced && r.report != nil {
		r.report(refusal)
	}
}

// lacksPermission is true for a user missing what the route needs. Public
// routes and service calls are outside it.
func (r *Routes) lacksPermission(req *http.Request, caller Audience, audiences []Audience, need Requirement) bool {
	if caller == Public || caller == Service || slices.Contains(audiences, Public) {
		return false
	}
	claims, ok := ClaimsFromRequest(req)
	return !ok || !permitted(claims, need, req.Method)
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
// nobody the request proves. A portal it does not know is nobody: falling
// back to tenant would let any other portal's token onto tenant routes.
func CallerAudience(req *http.Request) Audience {
	claims, ok := ClaimsFromRequest(req)
	switch {
	case !ok || claims == nil:
		return Public
	case claims.Service != "":
		return Service
	case claims.Portal == identity.PortalAdmin:
		return Admin
	case claims.Portal == identity.PortalTenant:
		return Tenant
	case claims.Portal == identity.PortalInstaller:
		return Installer
	default:
		return Public
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
	f.routes.refused(f.req, f.caller, "forbidden", f.routes.enforce)
	if !f.routes.enforce {
		f.ResponseWriter.WriteHeader(status)
		return
	}
	f.swallow = true
	NotFound(f.ResponseWriter, f.req)
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
