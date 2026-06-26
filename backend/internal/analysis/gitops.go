package analysis

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// GitOpsIssueType identifies a category of ArgoCD application problem.
type GitOpsIssueType string

const (
	IssueSyncFailed    GitOpsIssueType = "SyncFailed"
	IssueAppDegraded   GitOpsIssueType = "ApplicationDegraded"
	IssueOutOfSync     GitOpsIssueType = "OutOfSync"
	IssueHealthMissing GitOpsIssueType = "HealthMissing"
	IssueAppCondition  GitOpsIssueType = "ApplicationCondition"
)

// AppResourceStatus is the sync/health of one resource managed by an Application.
type AppResourceStatus struct {
	Group        string `json:"group,omitempty"`
	Kind         string `json:"kind"`
	Namespace    string `json:"namespace,omitempty"`
	Name         string `json:"name"`
	SyncStatus   string `json:"syncStatus,omitempty"`
	HealthStatus string `json:"healthStatus,omitempty"`
}

// AppCondition mirrors an entry in Application.status.conditions.
type AppCondition struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
}

// ArgoApplication is the distilled, scoring-relevant view of an ArgoCD
// Application custom resource.
type ArgoApplication struct {
	Namespace        string              `json:"namespace"`
	Name             string              `json:"name"`
	Project          string              `json:"project,omitempty"`
	SyncStatus       string              `json:"syncStatus"`   // Synced | OutOfSync | Unknown
	HealthStatus     string              `json:"healthStatus"` // Healthy | Degraded | Progressing | Missing | Suspended | Unknown
	OperationPhase   string              `json:"operationPhase,omitempty"`
	OperationMessage string              `json:"operationMessage,omitempty"`
	Conditions       []AppCondition      `json:"conditions,omitempty"`
	Resources        []AppResourceStatus `json:"resources,omitempty"`
}

// GitOpsSnapshot is the raw observed state the GitOps rules evaluate.
type GitOpsSnapshot struct {
	ClusterID          string            `json:"clusterId"`
	Namespace          string            `json:"namespace"`
	APIServerReachable bool              `json:"apiServerReachable"`
	APIServerError     string            `json:"apiServerError,omitempty"`
	ArgoCDInstalled    bool              `json:"argoCdInstalled"`
	Applications       []ArgoApplication `json:"applications"`
	CollectedAt        time.Time         `json:"collectedAt"`
}

// GitOpsFinding is a single deterministic ArgoCD application issue.
type GitOpsFinding struct {
	Type        GitOpsIssueType `json:"type"`
	Severity    Severity        `json:"severity"`
	Namespace   string          `json:"namespace"`
	Application string          `json:"application"`
	Message     string          `json:"message"`
	Details     map[string]any  `json:"details,omitempty"`
}

// GitOpsSummary is a quick-glance rollup of fleet sync/health.
type GitOpsSummary struct {
	ArgoCDInstalled    bool                    `json:"argoCdInstalled"`
	TotalApplications  int                     `json:"totalApplications"`
	Synced             int                     `json:"synced"`
	OutOfSync          int                     `json:"outOfSync"`
	Healthy            int                     `json:"healthy"`
	Degraded           int                     `json:"degraded"`
	FindingsBySeverity map[Severity]int        `json:"findingsBySeverity"`
	FindingsByType     map[GitOpsIssueType]int `json:"findingsByType"`
}

// GitOpsReport is the full result of a GitOps-analysis run.
type GitOpsReport struct {
	ClusterID   string          `json:"clusterId"`
	Namespace   string          `json:"namespace"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Summary     GitOpsSummary   `json:"summary"`
	Findings    []GitOpsFinding `json:"findings"`
}

// AnalyzeGitOps evaluates the deterministic GitOps rule set over a snapshot. It
// performs no I/O. When ArgoCD is not installed it returns an empty, clean
// report (absence of ArgoCD is not a finding).
func AnalyzeGitOps(snap GitOpsSnapshot) GitOpsReport {
	report := GitOpsReport{
		ClusterID:   snap.ClusterID,
		Namespace:   snap.Namespace,
		GeneratedAt: time.Now().UTC(),
		Findings:    make([]GitOpsFinding, 0),
		Summary:     GitOpsSummary{ArgoCDInstalled: snap.ArgoCDInstalled},
	}
	if !snap.ArgoCDInstalled {
		return report
	}

	report.Summary.TotalApplications = len(snap.Applications)
	for _, app := range snap.Applications {
		switch app.SyncStatus {
		case "Synced":
			report.Summary.Synced++
		case "OutOfSync":
			report.Summary.OutOfSync++
		}
		switch app.HealthStatus {
		case "Healthy":
			report.Summary.Healthy++
		case "Degraded":
			report.Summary.Degraded++
		}
		report.Findings = append(report.Findings, evaluateApplication(app)...)
	}

	report.Summary.FindingsBySeverity = gitopsCountBySeverity(report.Findings)
	report.Summary.FindingsByType = gitopsCountByType(report.Findings)
	sortGitOpsFindings(report.Findings)
	return report
}

func evaluateApplication(app ArgoApplication) []GitOpsFinding {
	var out []GitOpsFinding

	// A failed sync operation is the most urgent: the desired state did not
	// apply, so the cluster silently diverges from Git.
	if app.OperationPhase == "Failed" || app.OperationPhase == "Error" {
		out = append(out, gitopsFinding(app, IssueSyncFailed, SeverityCritical,
			"last sync operation "+strings.ToLower(app.OperationPhase)+"; cluster is diverging from Git",
			pruneEmpty(map[string]any{"phase": app.OperationPhase, "message": app.OperationMessage})))
	}

	if app.HealthStatus == "Degraded" {
		out = append(out, gitopsFinding(app, IssueAppDegraded, SeverityCritical,
			"application health is Degraded", nil))
	}

	if app.SyncStatus == "OutOfSync" {
		drifted := driftedResources(app)
		out = append(out, gitopsFinding(app, IssueOutOfSync, SeverityWarning,
			fmt.Sprintf("application is OutOfSync (%d drifted resource(s))", len(drifted)),
			pruneEmpty(map[string]any{"driftedResources": drifted})))
	}

	// Missing means resources the Application should manage are absent; Unknown
	// means ArgoCD could not assess health. Missing is the more actionable.
	switch app.HealthStatus {
	case "Missing":
		out = append(out, gitopsFinding(app, IssueHealthMissing, SeverityWarning,
			"application health is Missing; managed resources may not exist", nil))
	case "Unknown":
		out = append(out, gitopsFinding(app, IssueHealthMissing, SeverityInfo,
			"application health is Unknown; ArgoCD could not assess it", nil))
	}

	for _, cond := range app.Conditions {
		if sev, ok := conditionSeverity(cond.Type); ok {
			out = append(out, gitopsFinding(app, IssueAppCondition, sev,
				"application condition: "+cond.Type,
				pruneEmpty(map[string]any{"conditionType": cond.Type, "message": cond.Message})))
		}
	}
	return out
}

// driftedResources returns the resources reported OutOfSync within an app.
func driftedResources(app ArgoApplication) []string {
	var out []string
	for _, r := range app.Resources {
		if r.SyncStatus == "OutOfSync" {
			out = append(out, resourceLabel(r))
		}
	}
	sort.Strings(out)
	return out
}

func resourceLabel(r AppResourceStatus) string {
	id := r.Kind + "/" + r.Name
	if r.Namespace != "" {
		id = r.Namespace + "/" + id
	}
	return id
}

// conditionSeverity classifies an ArgoCD condition type. *Error conditions are
// warnings (they usually accompany a separate failure finding); *Warning
// conditions are informational.
func conditionSeverity(condType string) (Severity, bool) {
	lt := strings.ToLower(condType)
	switch {
	case strings.Contains(lt, "error"):
		return SeverityWarning, true
	case strings.Contains(lt, "warning"):
		return SeverityInfo, true
	default:
		return "", false
	}
}

func gitopsFinding(app ArgoApplication, t GitOpsIssueType, sev Severity, msg string, details map[string]any) GitOpsFinding {
	return GitOpsFinding{
		Type: t, Severity: sev,
		Namespace: app.Namespace, Application: app.Name,
		Message: msg, Details: details,
	}
}

func sortGitOpsFindings(f []GitOpsFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if r := severityRank(a.Severity) - severityRank(b.Severity); r != 0 {
			return r < 0
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Application != b.Application {
			return a.Application < b.Application
		}
		return a.Type < b.Type
	})
}

func gitopsCountBySeverity(f []GitOpsFinding) map[Severity]int {
	m := map[Severity]int{}
	for _, x := range f {
		m[x.Severity]++
	}
	return m
}

func gitopsCountByType(f []GitOpsFinding) map[GitOpsIssueType]int {
	m := map[GitOpsIssueType]int{}
	for _, x := range f {
		m[x.Type]++
	}
	return m
}
