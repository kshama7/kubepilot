package analysis

import (
	"testing"
	"time"
)

func runningPod(ns, name string, containers ...ContainerState) PodStatus {
	return PodStatus{
		Namespace: ns, Name: name, Phase: "Running",
		Scheduled: true, NodeName: "worker-0",
		CreatedAt:  time.Now().Add(-time.Hour),
		Containers: containers,
	}
}

func TestAnalyzeWorkloads_AllHealthy(t *testing.T) {
	snap := WorkloadSnapshot{
		ClusterID:          "kind-dev",
		APIServerReachable: true,
		CollectedAt:        time.Now(),
		Pods: []PodStatus{
			runningPod("default", "web-0", ContainerState{Name: "web", Ready: true, Started: true}),
			runningPod("default", "web-1", ContainerState{Name: "web", Ready: true, Started: true}),
		},
	}
	got := AnalyzeWorkloads(snap)
	if len(got.Findings) != 0 {
		t.Fatalf("expected no findings, got %d: %+v", len(got.Findings), got.Findings)
	}
	if got.Summary.HealthyPods != 2 || got.Summary.PodsWithIssues != 0 {
		t.Fatalf("unexpected summary: %+v", got.Summary)
	}
}

func TestAnalyzeWorkloads_CrashLoop(t *testing.T) {
	pod := runningPod("prod", "api-0", ContainerState{
		Name: "api", Started: true, Ready: false,
		WaitingReason: "CrashLoopBackOff", RestartCount: 3,
	})
	got := AnalyzeWorkloads(WorkloadSnapshot{ClusterID: "c", CollectedAt: time.Now(), Pods: []PodStatus{pod}})

	f := requireFinding(t, got, IssueCrashLoopBackOff)
	if f.Severity != SeverityCritical || f.Container != "api" {
		t.Fatalf("unexpected crashloop finding: %+v", f)
	}
	// Not-ready must be suppressed while crashlooping.
	if hasType(got, IssueNotReady) {
		t.Fatal("should not emit NotReady alongside CrashLoopBackOff")
	}
}

func TestAnalyzeWorkloads_OOMAndRestartStorm(t *testing.T) {
	pod := runningPod("prod", "worker-0", ContainerState{
		Name: "worker", Started: true, Ready: false,
		WaitingReason:        "CrashLoopBackOff",
		LastTerminatedReason: "OOMKilled",
		RestartCount:         25,
	})
	got := AnalyzeWorkloads(WorkloadSnapshot{ClusterID: "c", CollectedAt: time.Now(), Pods: []PodStatus{pod}})

	// One container legitimately yields three distinct signals.
	requireFinding(t, got, IssueCrashLoopBackOff)
	requireFinding(t, got, IssueOOMKilled)
	storm := requireFinding(t, got, IssueRestartStorm)
	if storm.Severity != SeverityCritical {
		t.Fatalf("25 restarts should escalate restart storm to critical, got %q", storm.Severity)
	}
	if got.Summary.PodsWithIssues != 1 {
		t.Fatalf("expected 1 pod with issues, got %d", got.Summary.PodsWithIssues)
	}
}

func TestAnalyzeWorkloads_RestartStormWarningVsCritical(t *testing.T) {
	pod := runningPod("default", "flaky", ContainerState{
		Name: "app", Started: true, Ready: true, RestartCount: 7,
	})
	got := AnalyzeWorkloads(WorkloadSnapshot{ClusterID: "c", CollectedAt: time.Now(), Pods: []PodStatus{pod}})
	storm := requireFinding(t, got, IssueRestartStorm)
	if storm.Severity != SeverityWarning {
		t.Fatalf("7 restarts should be a warning, got %q", storm.Severity)
	}
}

func TestAnalyzeWorkloads_ImagePull(t *testing.T) {
	pod := PodStatus{
		Namespace: "default", Name: "bad-image", Phase: "Pending", Scheduled: true,
		CreatedAt: time.Now(),
		Containers: []ContainerState{{
			Name: "app", WaitingReason: "ImagePullBackOff",
			WaitingMessage: "Back-off pulling image \"nope:latest\"",
		}},
	}
	got := AnalyzeWorkloads(WorkloadSnapshot{ClusterID: "c", CollectedAt: time.Now(), Pods: []PodStatus{pod}})
	f := requireFinding(t, got, IssueImagePull)
	if f.Severity != SeverityCritical {
		t.Fatalf("image pull error should be critical, got %q", f.Severity)
	}
}

func TestAnalyzeWorkloads_Unschedulable(t *testing.T) {
	pod := PodStatus{
		Namespace: "default", Name: "pending-0", Phase: "Pending",
		Scheduled: false, UnschedulableReason: "Unschedulable",
		UnschedulableMessage: "0/3 nodes are available: 3 Insufficient cpu.",
		CreatedAt:            time.Now(),
	}
	got := AnalyzeWorkloads(WorkloadSnapshot{ClusterID: "c", CollectedAt: time.Now(), Pods: []PodStatus{pod}})
	f := requireFinding(t, got, IssueUnschedulable)
	if f.Severity != SeverityCritical {
		t.Fatalf("unschedulable should be critical, got %q", f.Severity)
	}
	if f.Details["reason"] != "Unschedulable" {
		t.Fatalf("expected reason detail, got %+v", f.Details)
	}
}

func TestAnalyzeWorkloads_PendingGracePeriod(t *testing.T) {
	now := time.Now()
	// Fresh pending pod (within grace) → no finding.
	fresh := PodStatus{Namespace: "d", Name: "fresh", Phase: "Pending", Scheduled: true, CreatedAt: now.Add(-time.Minute)}
	// Old pending pod (past grace) → warning.
	stuck := PodStatus{Namespace: "d", Name: "stuck", Phase: "Pending", Scheduled: true, CreatedAt: now.Add(-10 * time.Minute)}

	got := AnalyzeWorkloads(WorkloadSnapshot{ClusterID: "c", CollectedAt: now, Pods: []PodStatus{fresh, stuck}})
	if hasFindingForPod(got, "fresh") {
		t.Fatal("fresh pending pod within grace should not be flagged")
	}
	f := requireFinding(t, got, IssuePendingStuck)
	if f.Pod != "stuck" {
		t.Fatalf("expected stuck pod flagged, got %q", f.Pod)
	}
}

func TestAnalyzeWorkloads_NotReadyReadinessProbe(t *testing.T) {
	pod := runningPod("default", "web", ContainerState{Name: "web", Started: true, Ready: false})
	got := AnalyzeWorkloads(WorkloadSnapshot{ClusterID: "c", CollectedAt: time.Now(), Pods: []PodStatus{pod}})
	f := requireFinding(t, got, IssueNotReady)
	if f.Severity != SeverityWarning {
		t.Fatalf("not-ready should be warning, got %q", f.Severity)
	}
}

func TestAnalyzeWorkloads_SortingCriticalFirst(t *testing.T) {
	pods := []PodStatus{
		runningPod("default", "warn", ContainerState{Name: "c", Started: true, Ready: false}), // warning NotReady
		runningPod("default", "crit", ContainerState{Name: "c", Started: true, WaitingReason: "CrashLoopBackOff"}),
	}
	got := AnalyzeWorkloads(WorkloadSnapshot{ClusterID: "c", CollectedAt: time.Now(), Pods: pods})
	if len(got.Findings) < 2 {
		t.Fatalf("expected at least 2 findings, got %d", len(got.Findings))
	}
	if got.Findings[0].Severity != SeverityCritical {
		t.Fatalf("critical findings must sort first, got %q", got.Findings[0].Severity)
	}
}

// --- helpers ---

func requireFinding(t *testing.T, r WorkloadReport, typ WorkloadIssueType) WorkloadFinding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Type == typ {
			return f
		}
	}
	t.Fatalf("expected a %q finding, got %+v", typ, r.Findings)
	return WorkloadFinding{}
}

func hasType(r WorkloadReport, typ WorkloadIssueType) bool {
	for _, f := range r.Findings {
		if f.Type == typ {
			return true
		}
	}
	return false
}

func hasFindingForPod(r WorkloadReport, pod string) bool {
	for _, f := range r.Findings {
		if f.Pod == pod {
			return true
		}
	}
	return false
}
