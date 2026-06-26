package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/kshama7/kubepilot/backend/internal/ai"
	"github.com/kshama7/kubepilot/backend/internal/analysis"
)

// handleExplain runs a deterministic analyzer and asks the AI layer to explain
// its findings. The AI never runs free-standing — it only ever receives findings
// the rule engine produced in this same request.
//
// GET /api/v1/clusters/{id}/explain?analyzer=workload[&namespace=...][&target=...]
func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
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
	if s.explainer == nil || !s.explainer.Enabled() {
		writeError(w, http.StatusServiceUnavailable,
			"AI explanation is not configured; set KUBEPILOT_ANTHROPIC_API_KEY")
		return
	}

	analyzer := r.URL.Query().Get("analyzer")
	if analyzer == "" {
		writeError(w, http.StatusBadRequest, "analyzer query parameter is required (e.g. ?analyzer=workload)")
		return
	}
	namespace := r.URL.Query().Get("namespace")
	target := r.URL.Query().Get("target")

	// Step 1: deterministic analysis. The findings are the ONLY thing the model
	// is allowed to discuss.
	gathered, ctxSummary, collErr := s.gatherFindings(r.Context(), analyzer, clusterID, namespace, target)
	if collErr != nil {
		writeError(w, collErr.status, collErr.message)
		return
	}

	if len(gathered) == 0 {
		// Nothing to explain — and nothing the model is permitted to invent.
		writeJSON(w, http.StatusOK, map[string]any{
			"analyzer":          analyzer,
			"clusterId":         clusterID,
			"findingsExplained": 0,
			"explanation":       "No findings were produced by the " + analyzer + " analyzer; nothing to explain.",
		})
		return
	}

	// Step 2: AI explanation over the deterministic findings.
	start := time.Now()
	resp, err := s.explainer.Explain(r.Context(), ai.ExplainRequest{
		ClusterID: clusterID,
		Analyzer:  analyzer,
		Context:   ctxSummary,
		Findings:  gathered,
	})
	elapsed := time.Since(start)

	if err != nil {
		if errors.Is(err, ai.ErrDisabled) {
			writeError(w, http.StatusServiceUnavailable, "AI explanation is not configured")
			return
		}
		s.metrics.AnalysisDuration.WithLabelValues("explain", "error").Observe(elapsed.Seconds())
		s.log.Error("ai explanation failed", zap.String("analyzer", analyzer), zap.Error(err))
		writeError(w, http.StatusBadGateway, "AI explanation failed: "+err.Error())
		return
	}

	s.metrics.AnalysisDuration.WithLabelValues("explain", "success").Observe(elapsed.Seconds())
	s.log.Info("findings explained",
		zap.String("cluster_id", clusterID),
		zap.String("analyzer", analyzer),
		zap.Int("findings", resp.FindingsExplained),
		zap.String("model", resp.Model),
		zap.Duration("duration", elapsed),
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"analyzer":          analyzer,
		"clusterId":         clusterID,
		"model":             resp.Model,
		"findingsExplained": resp.FindingsExplained,
		"context":           ctxSummary,
		"explanation":       resp.Explanation,
		"generatedAt":       time.Now().UTC(),
	})
}

// collectionError carries an HTTP status for a failed collection.
type collectionError struct {
	status  int
	message string
}

// gatherFindings dispatches to the requested analyzer, collects + scores, and
// reduces the report to the analyzer-agnostic []ai.Finding the explainer needs.
func (s *Server) gatherFindings(ctx context.Context, analyzer, clusterID, namespace, target string) ([]ai.Finding, string, *collectionError) {
	switch analyzer {
	case "cluster_health":
		snap := s.collector.CollectClusterSnapshot(ctx, clusterID)
		report := analysis.ScoreClusterHealth(snap)
		var out []ai.Finding
		for _, c := range report.Checks {
			if !c.Passed {
				out = append(out, ai.Finding{
					Analyzer: analyzer, Type: c.ID, Severity: string(c.Severity),
					Resource: clusterID, Message: c.Message,
				})
			}
		}
		return out, fmt.Sprintf("health score %d/100 (%s)", report.Score, report.Status), nil

	case "workload":
		snap := s.collector.CollectWorkloadSnapshot(ctx, clusterID, namespace)
		if !snap.APIServerReachable {
			return nil, "", &collectionError{http.StatusBadGateway, "could not list pods: " + snap.APIServerError}
		}
		report := analysis.AnalyzeWorkloads(snap)
		out := make([]ai.Finding, 0, len(report.Findings))
		for _, f := range report.Findings {
			out = append(out, ai.Finding{
				Analyzer: analyzer, Type: string(f.Type), Severity: string(f.Severity),
				Resource: resourceID(f.Namespace, f.Pod, f.Container), Message: f.Message,
			})
		}
		return out, fmt.Sprintf("%d pods, %d findings", report.Summary.TotalPods, len(report.Findings)), nil

	case "resource":
		snap := s.collector.CollectResourceSnapshot(ctx, clusterID, namespace)
		if !snap.APIServerReachable {
			return nil, "", &collectionError{http.StatusBadGateway, "could not list pods: " + snap.APIServerError}
		}
		report := analysis.AnalyzeResources(snap)
		out := make([]ai.Finding, 0, len(report.Findings))
		for _, f := range report.Findings {
			out = append(out, ai.Finding{
				Analyzer: analyzer, Type: string(f.Type), Severity: string(f.Severity),
				Resource: resourceID(f.Namespace, f.Pod, f.Container), Message: f.Message,
			})
		}
		return out, fmt.Sprintf("%d containers, metrics_available=%v", report.Summary.TotalContainers, report.Summary.MetricsAvailable), nil

	case "reliability":
		snap := s.collector.CollectReliabilitySnapshot(ctx, clusterID, namespace)
		if !snap.APIServerReachable {
			return nil, "", &collectionError{http.StatusBadGateway, "could not list workloads: " + snap.APIServerError}
		}
		report := analysis.AnalyzeReliability(snap)
		out := make([]ai.Finding, 0, len(report.Findings))
		for _, f := range report.Findings {
			out = append(out, ai.Finding{
				Analyzer: analyzer, Type: string(f.Type), Severity: string(f.Severity),
				Resource: resourceID(f.Namespace, f.Kind+"/"+f.Workload, f.Container), Message: f.Message,
			})
		}
		return out, fmt.Sprintf("%d workloads, %d findings", report.Summary.TotalWorkloads, len(report.Findings)), nil

	case "upgrade":
		snap := s.collector.CollectUpgradeSnapshot(ctx, clusterID)
		if !snap.APIServerReachable {
			return nil, "", &collectionError{http.StatusBadGateway, "could not reach API server: " + snap.APIServerError}
		}
		report := analysis.AnalyzeUpgrade(snap, target)
		out := make([]ai.Finding, 0, len(report.Findings))
		for _, f := range report.Findings {
			out = append(out, ai.Finding{
				Analyzer: analyzer, Type: string(f.Type), Severity: string(f.Severity),
				Resource: f.APIVersion + "/" + f.Kind, Message: f.Message,
			})
		}
		return out, fmt.Sprintf("current %s, target %s", report.CurrentVersion, report.TargetVersion), nil

	case "gitops":
		snap := s.collector.CollectGitOpsSnapshot(ctx, clusterID, namespace)
		if !snap.APIServerReachable {
			return nil, "", &collectionError{http.StatusBadGateway, "could not reach API server: " + snap.APIServerError}
		}
		report := analysis.AnalyzeGitOps(snap)
		out := make([]ai.Finding, 0, len(report.Findings))
		for _, f := range report.Findings {
			out = append(out, ai.Finding{
				Analyzer: analyzer, Type: string(f.Type), Severity: string(f.Severity),
				Resource: resourceID(f.Namespace, f.Application, ""), Message: f.Message,
			})
		}
		ctxStr := fmt.Sprintf("argocd_installed=%v, %d applications", report.Summary.ArgoCDInstalled, report.Summary.TotalApplications)
		return out, ctxStr, nil

	case "security":
		snap := s.collector.CollectSecuritySnapshot(ctx, clusterID, namespace)
		if !snap.APIServerReachable {
			return nil, "", &collectionError{http.StatusBadGateway, "could not list pods: " + snap.APIServerError}
		}
		report := analysis.AnalyzeSecurity(snap)
		out := make([]ai.Finding, 0, len(report.Findings))
		for _, f := range report.Findings {
			out = append(out, ai.Finding{
				Analyzer: analyzer, Type: string(f.Type), Severity: string(f.Severity),
				Resource: resourceID(f.Namespace, f.Pod, f.Container), Message: f.Message,
			})
		}
		return out, fmt.Sprintf("%d pods, %d privileged", report.Summary.TotalPods, report.Summary.PrivilegedPods), nil

	case "capacity":
		snap := s.collector.CollectCapacitySnapshot(ctx, clusterID)
		if !snap.APIServerReachable {
			return nil, "", &collectionError{http.StatusBadGateway, "could not list nodes: " + snap.APIServerError}
		}
		report := analysis.AnalyzeCapacity(snap)
		out := make([]ai.Finding, 0, len(report.Findings))
		for _, f := range report.Findings {
			out = append(out, ai.Finding{
				Analyzer: analyzer, Type: string(f.Type), Severity: string(f.Severity),
				Resource: f.Node, Message: f.Message,
			})
		}
		return out, fmt.Sprintf("%d nodes, prometheus_available=%v", report.Summary.TotalNodes, report.Summary.PrometheusAvailable), nil

	default:
		return nil, "", &collectionError{http.StatusBadRequest,
			"unknown analyzer; valid values: cluster_health, workload, resource, reliability, upgrade, gitops, security, capacity"}
	}
}

// resourceID builds a stable "ns/name[/container]" identifier for a finding.
func resourceID(namespace, name, container string) string {
	id := name
	if namespace != "" {
		id = namespace + "/" + name
	}
	if container != "" {
		id += "/" + container
	}
	return id
}
