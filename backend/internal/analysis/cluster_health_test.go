package analysis

import "testing"

func node(name string, ready, schedulable bool) NodeStatus {
	return NodeStatus{Name: name, Ready: ready, Schedulable: schedulable}
}

func TestScoreClusterHealth_AllHealthy(t *testing.T) {
	snap := ClusterSnapshot{
		ClusterID:          "kind-dev",
		APIServerReachable: true,
		ServerVersion:      "v1.31.0",
		Nodes: []NodeStatus{
			node("cp-0", true, true),
			node("worker-0", true, true),
			node("worker-1", true, true),
		},
	}

	got := ScoreClusterHealth(snap)
	if got.Score != 100 {
		t.Fatalf("expected score 100 for fully healthy cluster, got %d", got.Score)
	}
	if got.Status != StatusHealthy {
		t.Fatalf("expected status %q, got %q", StatusHealthy, got.Status)
	}
	if got.Summary.FailedChecks != 0 {
		t.Fatalf("expected 0 failed checks, got %d", got.Summary.FailedChecks)
	}
}

func TestScoreClusterHealth_APIServerUnreachable(t *testing.T) {
	snap := ClusterSnapshot{
		ClusterID:          "kind-dev",
		APIServerReachable: false,
		APIServerError:     "dial tcp 127.0.0.1:6443: connect: connection refused",
	}

	got := ScoreClusterHealth(snap)
	// API unreachable (-35) and zero nodes fails readiness (-35) → 30.
	if got.Score != 30 {
		t.Fatalf("expected score 30 for unreachable API with no nodes, got %d", got.Score)
	}
	if got.Status != StatusCritical {
		t.Fatalf("expected critical status, got %q", got.Status)
	}

	api := findCheck(t, got, "control-plane-reachable")
	if api.Passed || api.Severity != SeverityCritical {
		t.Fatalf("api check should fail critically, got passed=%v severity=%q", api.Passed, api.Severity)
	}
}

func TestScoreClusterHealth_OneNodeNotReady(t *testing.T) {
	snap := ClusterSnapshot{
		ClusterID:          "kind-dev",
		APIServerReachable: true,
		Nodes: []NodeStatus{
			node("worker-0", true, true),
			node("worker-1", true, true),
			node("worker-2", false, true), // 1 of 3 not ready
		},
	}

	got := ScoreClusterHealth(snap)
	// readiness penalty = round(1/3 * 35) = 12 → score 88.
	if got.Score != 88 {
		t.Fatalf("expected score 88 with 1/3 nodes NotReady, got %d", got.Score)
	}
	if got.Status != StatusDegraded {
		t.Fatalf("expected degraded status, got %q", got.Status)
	}
	check := findCheck(t, got, "node-readiness")
	if check.Severity != SeverityCritical {
		t.Fatalf("1/3 not-ready should escalate to critical, got %q", check.Severity)
	}
}

func TestScoreClusterHealth_PressureAndCordon(t *testing.T) {
	pressured := node("worker-1", true, true)
	pressured.MemoryPressure = true

	snap := ClusterSnapshot{
		ClusterID:          "kind-dev",
		APIServerReachable: true,
		Nodes: []NodeStatus{
			node("worker-0", true, true),
			pressured,                     // 1 of 3 under memory pressure
			node("worker-2", true, false), // 1 of 3 cordoned
		},
	}
	// 3 nodes: pressure 1/3 → round(1/3*20)=7; cordon 1/3 → round(1/3*10)=3.
	got := ScoreClusterHealth(snap)
	wantScore := 100 - 7 - 3
	if got.Score != wantScore {
		t.Fatalf("expected score %d, got %d", wantScore, got.Score)
	}
	cordon := findCheck(t, got, "node-schedulability")
	if cordon.Severity != SeverityWarning {
		t.Fatalf("cordon should be capped at warning, got %q", cordon.Severity)
	}
}

func TestScoreClusterHealth_EmptyButReachable(t *testing.T) {
	snap := ClusterSnapshot{
		ClusterID:          "kind-dev",
		APIServerReachable: true,
		Nodes:              nil,
	}
	got := ScoreClusterHealth(snap)
	// Only node-readiness fails (no nodes) → -35 → 65.
	if got.Score != 65 {
		t.Fatalf("expected score 65 for reachable cluster with no nodes, got %d", got.Score)
	}
}

func TestScaledPenalty(t *testing.T) {
	cases := []struct {
		failing, total, weight, want int
	}{
		{0, 3, 30, 0},
		{3, 3, 30, 30},
		{1, 3, 30, 10},
		{1, 4, 20, 5},
		{0, 0, 30, 0},
	}
	for _, c := range cases {
		if got := scaledPenalty(c.failing, c.total, c.weight); got != c.want {
			t.Errorf("scaledPenalty(%d,%d,%d)=%d, want %d", c.failing, c.total, c.weight, got, c.want)
		}
	}
}

func TestStatusForScore(t *testing.T) {
	cases := []struct {
		score int
		want  Status
	}{
		{100, StatusHealthy},
		{90, StatusHealthy},
		{89, StatusDegraded},
		{70, StatusDegraded},
		{69, StatusCritical},
		{0, StatusCritical},
	}
	for _, c := range cases {
		if got := statusForScore(c.score); got != c.want {
			t.Errorf("statusForScore(%d)=%q, want %q", c.score, got, c.want)
		}
	}
}

func findCheck(t *testing.T, r ClusterHealthReport, id string) HealthCheck {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q not found in report", id)
	return HealthCheck{}
}
