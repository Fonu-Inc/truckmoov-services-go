package infrastructure

import (
	"net/http"
	"net/http/httptest"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type PrometheusGateMetrics struct {
	registry     *prometheus.Registry
	blocked      *prometheus.CounterVec
	reloadFailed prometheus.Counter
}

func NewPrometheusGateMetrics() *PrometheusGateMetrics {
	registry := prometheus.NewRegistry()
	blocked := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tracking_ingest_blocked_total",
		Help: "Location messages dropped by the suspension gate, by reason.",
	}, []string{"reason"})
	reloadFailed := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tracking_gate_reload_failures_total",
		Help: "Failed reloads of the suspension lists.",
	})
	registry.MustRegister(blocked, reloadFailed)
	return &PrometheusGateMetrics{registry: registry, blocked: blocked, reloadFailed: reloadFailed}
}

func (m *PrometheusGateMetrics) Blocked(reason string) { m.blocked.WithLabelValues(reason).Inc() }

func (m *PrometheusGateMetrics) ReloadFailed() { m.reloadFailed.Inc() }

// Handler appends the gate counters to the service's own metrics output.
func (m *PrometheusGateMetrics) Handler(base http.Handler) http.Handler {
	own := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		base.ServeHTTP(recorder, r)
		for key, values := range recorder.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
		own.ServeHTTP(&bodyOnly{ResponseWriter: w}, r)
	})
}

type bodyOnly struct{ http.ResponseWriter }

func (b *bodyOnly) WriteHeader(int)     {}
func (b *bodyOnly) Header() http.Header { return http.Header{} }
