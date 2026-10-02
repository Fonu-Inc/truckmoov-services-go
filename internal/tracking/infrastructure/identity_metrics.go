package infrastructure

import "github.com/prometheus/client_golang/prometheus"

type PrometheusIdentityMetrics struct {
	snapshot    prometheus.Gauge
	readThrough *prometheus.CounterVec
	conflicts   prometheus.Counter
	quarantined prometheus.Counter
}

func NewPrometheusIdentityMetrics(registry *prometheus.Registry) *PrometheusIdentityMetrics {
	m := &PrometheusIdentityMetrics{
		snapshot: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tracking_identity_snapshot_devices",
			Help: "Devices in the last fleet identity snapshot.",
		}),
		readThrough: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tracking_identity_read_through_total",
			Help: "Single-IMEI fleet lookups on a cache miss, by result.",
		}, []string{"result"}),
		conflicts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tracking_identity_conflicts_total",
			Help: "Fleet identities skipped because another tenant already holds the IMEI in the cache.",
		}),
		quarantined: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tracking_ingest_quarantined_total",
			Help: "Location messages quarantined as unknown devices.",
		}),
	}
	registry.MustRegister(m.snapshot, m.readThrough, m.conflicts, m.quarantined)
	return m
}

func (m *PrometheusIdentityMetrics) SnapshotSize(n int) { m.snapshot.Set(float64(n)) }
func (m *PrometheusIdentityMetrics) ReadThrough(result string) {
	m.readThrough.WithLabelValues(result).Inc()
}
func (m *PrometheusIdentityMetrics) Conflict()    { m.conflicts.Inc() }
func (m *PrometheusIdentityMetrics) Quarantined() { m.quarantined.Inc() }
