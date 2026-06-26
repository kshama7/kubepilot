package analysis

import (
	"testing"
	"time"
)

func gitopsSnap(installed bool, apps ...ArgoApplication) GitOpsSnapshot {
	return GitOpsSnapshot{
		ClusterID: "c", APIServerReachable: true, ArgoCDInstalled: installed,
		CollectedAt: time.Now(), Applications: apps,
	}
}

func healthyApp(name string) ArgoApplication {
	return ArgoApplication{
		Namespace: "argocd", Name: name, Project: "default",
		SyncStatus: "Synced", HealthStatus: "Healthy", OperationPhase: "Succeeded",
	}
}

func TestAnalyzeGitOps_NotInstalled(t *testing.T) {
	got := AnalyzeGitOps(gitopsSnap(false))
	if len(got.Findings) != 0 {
		t.Fatalf("absent ArgoCD should produce no findings, got %+v", got.Findings)
	}
	if got.Summary.ArgoCDInstalled {
		t.Fatal("summary should report ArgoCD not installed")
	}
}

func TestAnalyzeGitOps_AllHealthy(t *testing.T) {
	got := AnalyzeGitOps(gitopsSnap(true, healthyApp("web"), healthyApp("api")))
	if len(got.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", got.Findings)
	}
	if got.Summary.Synced != 2 || got.Summary.Healthy != 2 {
		t.Fatalf("unexpected summary: %+v", got.Summary)
	}
}

func TestAnalyzeGitOps_SyncFailed(t *testing.T) {
	app := healthyApp("web")
	app.OperationPhase = "Failed"
	app.OperationMessage = "one or more objects failed to apply"
	got := AnalyzeGitOps(gitopsSnap(true, app))
	f := requireGitOpsFinding(t, got, IssueSyncFailed)
	if f.Severity != SeverityCritical {
		t.Fatalf("sync failure should be critical, got %q", f.Severity)
	}
	if f.Details["phase"] != "Failed" {
		t.Fatalf("expected phase detail, got %+v", f.Details)
	}
}

func TestAnalyzeGitOps_OutOfSyncListsDriftedResources(t *testing.T) {
	app := healthyApp("web")
	app.SyncStatus = "OutOfSync"
	app.Resources = []AppResourceStatus{
		{Kind: "Deployment", Namespace: "prod", Name: "web", SyncStatus: "OutOfSync"},
		{Kind: "Service", Namespace: "prod", Name: "web", SyncStatus: "Synced"},
		{Kind: "ConfigMap", Namespace: "prod", Name: "web-cfg", SyncStatus: "OutOfSync"},
	}
	got := AnalyzeGitOps(gitopsSnap(true, app))
	f := requireGitOpsFinding(t, got, IssueOutOfSync)
	if f.Severity != SeverityWarning {
		t.Fatalf("out-of-sync should be warning, got %q", f.Severity)
	}
	drifted, _ := f.Details["driftedResources"].([]string)
	if len(drifted) != 2 {
		t.Fatalf("expected 2 drifted resources, got %v", drifted)
	}
	if got.Summary.OutOfSync != 1 {
		t.Fatalf("expected 1 out-of-sync app, got %d", got.Summary.OutOfSync)
	}
}

func TestAnalyzeGitOps_Degraded(t *testing.T) {
	app := healthyApp("web")
	app.HealthStatus = "Degraded"
	got := AnalyzeGitOps(gitopsSnap(true, app))
	f := requireGitOpsFinding(t, got, IssueAppDegraded)
	if f.Severity != SeverityCritical {
		t.Fatalf("degraded should be critical, got %q", f.Severity)
	}
	if got.Summary.Degraded != 1 {
		t.Fatalf("expected 1 degraded app, got %d", got.Summary.Degraded)
	}
}

func TestAnalyzeGitOps_MissingVsUnknownHealth(t *testing.T) {
	missing := healthyApp("m")
	missing.HealthStatus = "Missing"
	unknown := healthyApp("u")
	unknown.HealthStatus = "Unknown"

	got := AnalyzeGitOps(gitopsSnap(true, missing, unknown))
	var missingSev, unknownSev Severity
	for _, f := range got.Findings {
		if f.Type == IssueHealthMissing && f.Application == "m" {
			missingSev = f.Severity
		}
		if f.Type == IssueHealthMissing && f.Application == "u" {
			unknownSev = f.Severity
		}
	}
	if missingSev != SeverityWarning {
		t.Fatalf("Missing health should be warning, got %q", missingSev)
	}
	if unknownSev != SeverityInfo {
		t.Fatalf("Unknown health should be info, got %q", unknownSev)
	}
}

func TestAnalyzeGitOps_ErrorCondition(t *testing.T) {
	app := healthyApp("web")
	app.Conditions = []AppCondition{
		{Type: "ComparisonError", Message: "rpc error: code = Unknown"},
		{Type: "OrphanedResourceWarning", Message: "1 orphaned resource"},
		{Type: "SomethingBenign", Message: "ignored"},
	}
	got := AnalyzeGitOps(gitopsSnap(true, app))

	var sawError, sawWarning bool
	for _, f := range got.Findings {
		if f.Type == IssueAppCondition && f.Details["conditionType"] == "ComparisonError" {
			sawError = f.Severity == SeverityWarning
		}
		if f.Type == IssueAppCondition && f.Details["conditionType"] == "OrphanedResourceWarning" {
			sawWarning = f.Severity == SeverityInfo
		}
		if f.Details != nil && f.Details["conditionType"] == "SomethingBenign" {
			t.Fatal("benign conditions should not produce findings")
		}
	}
	if !sawError {
		t.Fatal("expected ComparisonError as a warning finding")
	}
	if !sawWarning {
		t.Fatal("expected OrphanedResourceWarning as an info finding")
	}
}

func TestAnalyzeGitOps_SortingCriticalFirst(t *testing.T) {
	drift := healthyApp("drift")
	drift.SyncStatus = "OutOfSync" // warning
	fail := healthyApp("fail")
	fail.OperationPhase = "Error" // critical
	got := AnalyzeGitOps(gitopsSnap(true, drift, fail))
	if got.Findings[0].Severity != SeverityCritical {
		t.Fatalf("critical findings must sort first, got %q", got.Findings[0].Severity)
	}
}

func requireGitOpsFinding(t *testing.T, r GitOpsReport, typ GitOpsIssueType) GitOpsFinding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Type == typ {
			return f
		}
	}
	t.Fatalf("expected a %q finding, got %+v", typ, r.Findings)
	return GitOpsFinding{}
}
