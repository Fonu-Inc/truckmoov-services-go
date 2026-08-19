package httpx

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns a private Prometheus registry and the RED instruments for a
// service's HTTP surface. One is constructed per service and shared between the
// instrumenting middleware and the /metrics handler.
type Metrics struct {
	registry  *prometheus.Registry
	requests  *prometheus.CounterVec
	durations *prometheus.HistogramVec
}

// NewMetrics builds a registry seeded with the Go runtime/process collectors
// and the HTTP request counter + latency histogram, all tagged with the
// service name as a constant label.
func NewMetrics(service string) *Metrics {
	registry := prometheus.NewRegistry()
	constLabels := prometheus.Labels{"service": service}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:        "http_requests_total",
		Help:        "Total HTTP requests processed, labelled by method, route and status.",
		ConstLabels: constLabels,
	}, []string{"method", "route", "status"})

	durations := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:        "http_request_duration_seconds",
		Help:        "HTTP request latency in seconds, labelled by method, route and status.",
		Buckets:     prometheus.DefBuckets,
		ConstLabels: constLabels,
	}, []string{"method", "route", "status"})

	registry.MustRegister(requests, durations)

	return &Metrics{registry: registry, requests: requests, durations: durations}
}

// Handler serves the Prometheus exposition endpoint. It is operational and must
// be mounted outside any authentication gate.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Middleware records a request counter and latency histogram for every request
// that reaches the router. It must wrap the *http.ServeMux directly (innermost)
// so that r.Pattern — populated by the mux when it matches a route — is set on
// the same *http.Request this middleware observes, keeping the route label
// bounded to registered patterns instead of raw, high-cardinality paths.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		route := r.Pattern
		if route == "" {
			route = "other"
		}
		statusText := strconv.Itoa(status)

		m.requests.WithLabelValues(r.Method, route, statusText).Inc()
		m.durations.WithLabelValues(r.Method, route, statusText).Observe(time.Since(start).Seconds())
	})
}
