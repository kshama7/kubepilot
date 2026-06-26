// Package api wires KubePilot's HTTP surface: the chi router, middleware, and
// handlers that orchestrate collection (k8s) and scoring (analysis).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/kshama7/kubepilot/backend/internal/analysis"
	"github.com/kshama7/kubepilot/backend/internal/metrics"
)

// ClusterCollector is the subset of the k8s client the API depends on. Defining
// it here (consumer-side) keeps handlers testable with a stub.
type ClusterCollector interface {
	CollectClusterSnapshot(ctx context.Context, clusterID string) analysis.ClusterSnapshot
	CollectWorkloadSnapshot(ctx context.Context, clusterID, namespace string) analysis.WorkloadSnapshot
	CollectResourceSnapshot(ctx context.Context, clusterID, namespace string) analysis.ResourceSnapshot
	CollectReliabilitySnapshot(ctx context.Context, clusterID, namespace string) analysis.ReliabilitySnapshot
	CollectUpgradeSnapshot(ctx context.Context, clusterID string) analysis.UpgradeSnapshot
	CollectGitOpsSnapshot(ctx context.Context, clusterID, namespace string) analysis.GitOpsSnapshot
}

// Server holds handler dependencies. collector may be nil when no kubeconfig was
// resolvable at boot; affected endpoints return 503 rather than crashing.
type Server struct {
	log       *zap.Logger
	metrics   *metrics.Metrics
	collector ClusterCollector
	version   string
}

// NewServer constructs a Server. collector may be nil.
func NewServer(log *zap.Logger, m *metrics.Metrics, collector ClusterCollector, version string) *Server {
	return &Server{log: log, metrics: m, collector: collector, version: version}
}

// Router builds the chi router with all routes and middleware mounted.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.requestLogger)

	// Liveness/readiness and metrics are intentionally outside /api/v1 and
	// unversioned, matching standard kube probe and scrape conventions.
	r.Get("/healthz", s.handleHealthz)
	r.Handle("/metrics", s.metrics.Handler())

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/clusters/{id}/health", s.handleClusterHealth)
		r.Get("/clusters/{id}/workloads", s.handleWorkloads)
		r.Get("/clusters/{id}/resources", s.handleResources)
		r.Get("/clusters/{id}/reliability", s.handleReliability)
		r.Get("/clusters/{id}/upgrade", s.handleUpgrade)
		r.Get("/clusters/{id}/gitops", s.handleGitOps)
	})

	return r
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "kubepilot-api",
		"version": s.version,
	})
}

func (s *Server) handleClusterHealth(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}

	if s.collector == nil {
		writeError(w, http.StatusServiceUnavailable,
			"no Kubernetes client configured; set KUBEPILOT_KUBECONFIG or run in-cluster")
		return
	}

	start := time.Now()
	snap := s.collector.CollectClusterSnapshot(r.Context(), clusterID)
	report := analysis.ScoreClusterHealth(snap)
	elapsed := time.Since(start)

	outcome := "success"
	if !snap.APIServerReachable {
		outcome = "error"
	}
	s.metrics.AnalysisDuration.WithLabelValues("cluster_health", outcome).Observe(elapsed.Seconds())
	s.metrics.ClusterHealthScore.WithLabelValues(clusterID).Set(float64(report.Score))
	for _, c := range report.Checks {
		if !c.Passed {
			s.metrics.RecommendationsTotal.WithLabelValues("cluster_health", string(c.Severity)).Inc()
		}
	}

	s.log.Info("cluster health analyzed",
		zap.String("cluster_id", clusterID),
		zap.Int("score", report.Score),
		zap.String("status", string(report.Status)),
		zap.Duration("duration", elapsed),
	)

	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleWorkloads(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}
	if s.collector == nil {
		writeError(w, http.StatusServiceUnavailable,
			"no Kubernetes client configured; set KUBEPILOT_KUBECONFIG or run in-cluster")
		return
	}

	// Optional namespace filter; empty means all namespaces.
	namespace := r.URL.Query().Get("namespace")

	start := time.Now()
	snap := s.collector.CollectWorkloadSnapshot(r.Context(), clusterID, namespace)
	if !snap.APIServerReachable {
		s.metrics.AnalysisDuration.WithLabelValues("workload", "error").Observe(time.Since(start).Seconds())
		writeError(w, http.StatusBadGateway, "could not list pods: "+snap.APIServerError)
		return
	}
	report := analysis.AnalyzeWorkloads(snap)
	elapsed := time.Since(start)

	s.metrics.AnalysisDuration.WithLabelValues("workload", "success").Observe(elapsed.Seconds())
	for _, f := range report.Findings {
		s.metrics.RecommendationsTotal.WithLabelValues("workload", string(f.Severity)).Inc()
	}

	s.log.Info("workloads analyzed",
		zap.String("cluster_id", clusterID),
		zap.String("namespace", namespace),
		zap.Int("pods", report.Summary.TotalPods),
		zap.Int("findings", len(report.Findings)),
		zap.Duration("duration", elapsed),
	)

	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleResources(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}
	if s.collector == nil {
		writeError(w, http.StatusServiceUnavailable,
			"no Kubernetes client configured; set KUBEPILOT_KUBECONFIG or run in-cluster")
		return
	}

	namespace := r.URL.Query().Get("namespace")

	start := time.Now()
	snap := s.collector.CollectResourceSnapshot(r.Context(), clusterID, namespace)
	if !snap.APIServerReachable {
		s.metrics.AnalysisDuration.WithLabelValues("resource", "error").Observe(time.Since(start).Seconds())
		writeError(w, http.StatusBadGateway, "could not list pods: "+snap.APIServerError)
		return
	}
	report := analysis.AnalyzeResources(snap)
	elapsed := time.Since(start)

	s.metrics.AnalysisDuration.WithLabelValues("resource", "success").Observe(elapsed.Seconds())
	for _, f := range report.Findings {
		s.metrics.RecommendationsTotal.WithLabelValues("resource", string(f.Severity)).Inc()
	}

	s.log.Info("resources analyzed",
		zap.String("cluster_id", clusterID),
		zap.String("namespace", namespace),
		zap.Int("containers", report.Summary.TotalContainers),
		zap.Int("findings", len(report.Findings)),
		zap.Bool("metrics_available", report.Summary.MetricsAvailable),
		zap.Duration("duration", elapsed),
	)

	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleReliability(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}
	if s.collector == nil {
		writeError(w, http.StatusServiceUnavailable,
			"no Kubernetes client configured; set KUBEPILOT_KUBECONFIG or run in-cluster")
		return
	}

	namespace := r.URL.Query().Get("namespace")

	start := time.Now()
	snap := s.collector.CollectReliabilitySnapshot(r.Context(), clusterID, namespace)
	if !snap.APIServerReachable {
		s.metrics.AnalysisDuration.WithLabelValues("reliability", "error").Observe(time.Since(start).Seconds())
		writeError(w, http.StatusBadGateway, "could not list workloads: "+snap.APIServerError)
		return
	}
	report := analysis.AnalyzeReliability(snap)
	elapsed := time.Since(start)

	s.metrics.AnalysisDuration.WithLabelValues("reliability", "success").Observe(elapsed.Seconds())
	for _, f := range report.Findings {
		s.metrics.RecommendationsTotal.WithLabelValues("reliability", string(f.Severity)).Inc()
	}

	s.log.Info("reliability analyzed",
		zap.String("cluster_id", clusterID),
		zap.String("namespace", namespace),
		zap.Int("workloads", report.Summary.TotalWorkloads),
		zap.Int("findings", len(report.Findings)),
		zap.Duration("duration", elapsed),
	)

	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}
	if s.collector == nil {
		writeError(w, http.StatusServiceUnavailable,
			"no Kubernetes client configured; set KUBEPILOT_KUBECONFIG or run in-cluster")
		return
	}

	// Optional explicit target version, e.g. ?target=1.25. Empty defaults to the
	// next minor after the cluster's current version.
	target := r.URL.Query().Get("target")

	start := time.Now()
	snap := s.collector.CollectUpgradeSnapshot(r.Context(), clusterID)
	if !snap.APIServerReachable {
		s.metrics.AnalysisDuration.WithLabelValues("upgrade", "error").Observe(time.Since(start).Seconds())
		writeError(w, http.StatusBadGateway, "could not reach API server: "+snap.APIServerError)
		return
	}
	report := analysis.AnalyzeUpgrade(snap, target)
	elapsed := time.Since(start)

	s.metrics.AnalysisDuration.WithLabelValues("upgrade", "success").Observe(elapsed.Seconds())
	for _, f := range report.Findings {
		s.metrics.RecommendationsTotal.WithLabelValues("upgrade", string(f.Severity)).Inc()
	}

	s.log.Info("upgrade readiness analyzed",
		zap.String("cluster_id", clusterID),
		zap.String("current_version", report.CurrentVersion),
		zap.String("target_version", report.TargetVersion),
		zap.Int("removed_apis", report.Summary.RemovedAPIs),
		zap.Int("deprecated_apis", report.Summary.DeprecatedAPIs),
		zap.Duration("duration", elapsed),
	)

	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleGitOps(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster id is required")
		return
	}
	if s.collector == nil {
		writeError(w, http.StatusServiceUnavailable,
			"no Kubernetes client configured; set KUBEPILOT_KUBECONFIG or run in-cluster")
		return
	}

	namespace := r.URL.Query().Get("namespace")

	start := time.Now()
	snap := s.collector.CollectGitOpsSnapshot(r.Context(), clusterID, namespace)
	if !snap.APIServerReachable {
		s.metrics.AnalysisDuration.WithLabelValues("gitops", "error").Observe(time.Since(start).Seconds())
		writeError(w, http.StatusBadGateway, "could not reach API server: "+snap.APIServerError)
		return
	}
	report := analysis.AnalyzeGitOps(snap)
	elapsed := time.Since(start)

	s.metrics.AnalysisDuration.WithLabelValues("gitops", "success").Observe(elapsed.Seconds())
	for _, f := range report.Findings {
		s.metrics.RecommendationsTotal.WithLabelValues("gitops", string(f.Severity)).Inc()
	}

	s.log.Info("gitops analyzed",
		zap.String("cluster_id", clusterID),
		zap.String("namespace", namespace),
		zap.Bool("argocd_installed", report.Summary.ArgoCDInstalled),
		zap.Int("applications", report.Summary.TotalApplications),
		zap.Int("findings", len(report.Findings)),
		zap.Duration("duration", elapsed),
	)

	writeJSON(w, http.StatusOK, report)
}

// requestLogger logs each request and records API latency by route + status.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		s.metrics.APIRequestDuration.
			WithLabelValues(r.Method, route, statusClass(status)).
			Observe(time.Since(start).Seconds())

		s.log.Debug("http request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", status),
			zap.Duration("duration", time.Since(start)),
			zap.String("request_id", middleware.GetReqID(r.Context())),
		)
	})
}

func statusClass(status int) string {
	return strconv.Itoa(status/100) + "xx"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
