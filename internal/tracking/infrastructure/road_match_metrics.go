package infrastructure

import "github.com/prometheus/client_golang/prometheus"

type PrometheusRoadMatchMetrics struct {
	dropped prometheus.Counter
}

func NewPrometheusRoadMatchMetrics(registry *prometheus.Registry) *PrometheusRoadMatchMetrics {
	m := &PrometheusRoadMatchMetrics{dropped: prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tracking_road_match_dropped_total",
		Help: "Moving fixes not map-matched because every matching slot was busy.",
	})}
	registry.MustRegister(m.dropped)
	return m
}

func (m *PrometheusRoadMatchMetrics) Dropped() { m.dropped.Inc() }
