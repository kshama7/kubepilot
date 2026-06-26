// Package metrics owns KubePilot's Prometheus instrumentation.
//
// All metrics are registered on a private registry so /metrics exposes exactly
// what we declare here (plus the standard Go/process collectors) and tests can
// construct an isolated instance without global-state collisions.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "kubepilot"

// Metrics holds the application's Prometheus collectors.
type Metrics struct {
	reg *prometheus.Registry

	// AnalysisDuration tracks how long each analyzer takes, labeled by the
	// analyzer name and outcome (success|error).
	AnalysisDuration *prometheus.HistogramVec
	// ClusterHealthScore is the most recent 0..100 score per cluster.
	ClusterHealthScore *prometheus.GaugeVec
	// RecommendationsTotal counts findings emitted, by analyzer and severity.
	RecommendationsTotal *prometheus.CounterVec
	// APIRequestDuration tracks HTTP handler latency by route and status class.
	APIRequestDuration *prometheus.HistogramVec
}

// New constructs and registers all collectors on a fresh registry.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		reg: reg,
		AnalysisDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "analysis_duration_seconds",
			Help:      "Duration of a single analyzer run in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"analyzer", "outcome"}),
		ClusterHealthScore: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "cluster_health_score",
			Help:      "Most recent cluster health score (0-100).",
		}, []string{"cluster_id"}),
		RecommendationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "recommendations_total",
			Help:      "Total findings emitted by analyzers, by severity.",
		}, []string{"analyzer", "severity"}),
		APIRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "api_request_duration_seconds",
			Help:      "HTTP request latency by route and status class.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "route", "status"}),
	}

	reg.MustRegister(
		m.AnalysisDuration,
		m.ClusterHealthScore,
		m.RecommendationsTotal,
		m.APIRequestDuration,
	)
	return m
}

// Handler returns the HTTP handler exposing the registry at /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
