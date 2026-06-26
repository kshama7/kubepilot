// Package analysis holds KubePilot's deterministic, rule-based analyzers.
//
// The scoring functions in this package are intentionally pure: they take a
// snapshot of observed cluster state and return a structured report. Collection
// of that state (talking to the Kubernetes API) lives in the k8s package so the
// rule logic stays trivially unit-testable without a live cluster.
package analysis

import (
	"math"
	"sort"
	"time"
)

// Severity classifies the impact of a single health check failure.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Status is the rolled-up cluster verdict derived from the health score.
type Status string

const (
	StatusHealthy  Status = "healthy"
	StatusDegraded Status = "degraded"
	StatusCritical Status = "critical"
)

// NodeStatus is the distilled, scoring-relevant view of a single node. It is
// produced by the k8s collector from a corev1.Node so the analyzer never has to
// import client-go.
type NodeStatus struct {
	Name               string `json:"name"`
	Ready              bool   `json:"ready"`
	Schedulable        bool   `json:"schedulable"` // false when cordoned (spec.unschedulable)
	MemoryPressure     bool   `json:"memoryPressure"`
	DiskPressure       bool   `json:"diskPressure"`
	PIDPressure        bool   `json:"pidPressure"`
	NetworkUnavailable bool   `json:"networkUnavailable"`
	KubeletVersion     string `json:"kubeletVersion,omitempty"`
	CPUCapacityMilli   int64  `json:"cpuCapacityMilli,omitempty"`
	MemCapacityBytes   int64  `json:"memCapacityBytes,omitempty"`
}

// hasPressure reports whether the node is under any resource pressure condition.
func (n NodeStatus) hasPressure() bool {
	return n.MemoryPressure || n.DiskPressure || n.PIDPressure || n.NetworkUnavailable
}

// ClusterSnapshot is the raw observed state the cluster-health rules evaluate.
type ClusterSnapshot struct {
	ClusterID          string       `json:"clusterId"`
	APIServerReachable bool         `json:"apiServerReachable"`
	APIServerError     string       `json:"apiServerError,omitempty"`
	ServerVersion      string       `json:"serverVersion,omitempty"`
	Nodes              []NodeStatus `json:"nodes"`
	CollectedAt        time.Time    `json:"collectedAt"`
}

// HealthCheck is one deterministic rule evaluation contributing to the score.
type HealthCheck struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Passed   bool           `json:"passed"`
	Severity Severity       `json:"severity"`
	Message  string         `json:"message"`
	Weight   int            `json:"weight"`  // max points this check can deduct
	Penalty  int            `json:"penalty"` // points actually deducted (0..Weight)
	Details  map[string]any `json:"details,omitempty"`
}

// ClusterHealthSummary is a quick-glance rollup for the dashboard header.
type ClusterHealthSummary struct {
	TotalNodes    int `json:"totalNodes"`
	ReadyNodes    int `json:"readyNodes"`
	PressureNodes int `json:"pressureNodes"`
	CordonedNodes int `json:"cordonedNodes"`
	FailedChecks  int `json:"failedChecks"`
}

// ClusterHealthReport is the full result of a cluster-health analysis run.
type ClusterHealthReport struct {
	ClusterID   string               `json:"clusterId"`
	Score       int                  `json:"score"` // 0..100
	Status      Status               `json:"status"`
	GeneratedAt time.Time            `json:"generatedAt"`
	Summary     ClusterHealthSummary `json:"summary"`
	Checks      []HealthCheck        `json:"checks"`
}

// Check weights. They sum to 100 so a fully-failing cluster scores 0. The split
// reflects on-call reality: an unreachable control plane or unready nodes are
// far more urgent than a cordoned node during a planned drain.
const (
	weightAPIServer   = 35
	weightNodeReady   = 35
	weightNoPressure  = 20
	weightSchedulable = 10
)

// Score thresholds for the rolled-up status.
const (
	healthyThreshold  = 90
	degradedThreshold = 70
)

// ScoreClusterHealth evaluates the deterministic cluster-health rule set over a
// snapshot and returns a 0..100 report. It performs no I/O.
func ScoreClusterHealth(snap ClusterSnapshot) ClusterHealthReport {
	checks := []HealthCheck{
		apiServerCheck(snap),
		nodeReadyCheck(snap),
		noPressureCheck(snap),
		schedulableCheck(snap),
	}

	totalPenalty := 0
	failed := 0
	for _, c := range checks {
		totalPenalty += c.Penalty
		if !c.Passed {
			failed++
		}
	}

	score := clamp(100-totalPenalty, 0, 100)

	report := ClusterHealthReport{
		ClusterID:   snap.ClusterID,
		Score:       score,
		Status:      statusForScore(score),
		GeneratedAt: time.Now().UTC(),
		Summary:     summarize(snap, failed),
		Checks:      checks,
	}
	return report
}

func apiServerCheck(snap ClusterSnapshot) HealthCheck {
	c := HealthCheck{
		ID:     "control-plane-reachable",
		Name:   "API server reachability",
		Weight: weightAPIServer,
	}
	if snap.APIServerReachable {
		c.Passed = true
		c.Severity = SeverityInfo
		c.Penalty = 0
		c.Message = "Kubernetes API server is reachable"
		if snap.ServerVersion != "" {
			c.Details = map[string]any{"serverVersion": snap.ServerVersion}
		}
		return c
	}
	c.Passed = false
	c.Severity = SeverityCritical
	c.Penalty = weightAPIServer
	c.Message = "Kubernetes API server is unreachable"
	if snap.APIServerError != "" {
		c.Details = map[string]any{"error": snap.APIServerError}
	}
	return c
}

func nodeReadyCheck(snap ClusterSnapshot) HealthCheck {
	c := HealthCheck{
		ID:     "node-readiness",
		Name:   "Node readiness",
		Weight: weightNodeReady,
	}
	total := len(snap.Nodes)
	if total == 0 {
		// Reachable control plane with zero nodes means nothing can be
		// scheduled. An unreachable one already failed the API check; here we
		// still flag the absence of schedulable capacity.
		c.Passed = false
		c.Severity = SeverityCritical
		c.Penalty = weightNodeReady
		c.Message = "no nodes reported by the cluster"
		return c
	}

	notReady := make([]string, 0)
	for _, n := range snap.Nodes {
		if !n.Ready {
			notReady = append(notReady, n.Name)
		}
	}
	ready := total - len(notReady)
	c.Penalty = scaledPenalty(len(notReady), total, weightNodeReady)
	if len(notReady) == 0 {
		c.Passed = true
		c.Severity = SeverityInfo
		c.Message = "all nodes are Ready"
		return c
	}
	sort.Strings(notReady)
	c.Passed = false
	c.Severity = severityFromFraction(len(notReady), total)
	c.Message = "one or more nodes are NotReady"
	c.Details = map[string]any{
		"readyNodes":    ready,
		"totalNodes":    total,
		"notReadyNodes": notReady,
	}
	return c
}

func noPressureCheck(snap ClusterSnapshot) HealthCheck {
	c := HealthCheck{
		ID:     "resource-pressure",
		Name:   "Node resource pressure",
		Weight: weightNoPressure,
	}
	total := len(snap.Nodes)
	if total == 0 {
		c.Passed = true // nothing to evaluate; node-readiness already flagged it
		c.Severity = SeverityInfo
		c.Message = "no nodes to evaluate for pressure"
		return c
	}
	pressured := make([]string, 0)
	for _, n := range snap.Nodes {
		if n.hasPressure() {
			pressured = append(pressured, n.Name)
		}
	}
	c.Penalty = scaledPenalty(len(pressured), total, weightNoPressure)
	if len(pressured) == 0 {
		c.Passed = true
		c.Severity = SeverityInfo
		c.Message = "no nodes report memory, disk, PID, or network pressure"
		return c
	}
	sort.Strings(pressured)
	c.Passed = false
	c.Severity = severityFromFraction(len(pressured), total)
	c.Message = "one or more nodes report resource pressure"
	c.Details = map[string]any{
		"pressuredNodes": pressured,
		"totalNodes":     total,
	}
	return c
}

func schedulableCheck(snap ClusterSnapshot) HealthCheck {
	c := HealthCheck{
		ID:     "node-schedulability",
		Name:   "Node schedulability",
		Weight: weightSchedulable,
	}
	total := len(snap.Nodes)
	if total == 0 {
		c.Passed = true
		c.Severity = SeverityInfo
		c.Message = "no nodes to evaluate for schedulability"
		return c
	}
	cordoned := make([]string, 0)
	for _, n := range snap.Nodes {
		if !n.Schedulable {
			cordoned = append(cordoned, n.Name)
		}
	}
	c.Penalty = scaledPenalty(len(cordoned), total, weightSchedulable)
	if len(cordoned) == 0 {
		c.Passed = true
		c.Severity = SeverityInfo
		c.Message = "all nodes are schedulable"
		return c
	}
	sort.Strings(cordoned)
	c.Passed = false
	// Cordoning is routine during maintenance, so cap severity at warning.
	c.Severity = SeverityWarning
	c.Message = "one or more nodes are cordoned (unschedulable)"
	c.Details = map[string]any{
		"cordonedNodes": cordoned,
		"totalNodes":    total,
	}
	return c
}

func summarize(snap ClusterSnapshot, failedChecks int) ClusterHealthSummary {
	s := ClusterHealthSummary{TotalNodes: len(snap.Nodes), FailedChecks: failedChecks}
	for _, n := range snap.Nodes {
		if n.Ready {
			s.ReadyNodes++
		}
		if n.hasPressure() {
			s.PressureNodes++
		}
		if !n.Schedulable {
			s.CordonedNodes++
		}
	}
	return s
}

// scaledPenalty deducts points in proportion to the fraction of nodes failing a
// check, rounded to the nearest point and capped at the check's weight.
func scaledPenalty(failing, total, weight int) int {
	if total == 0 || failing == 0 {
		return 0
	}
	p := int(math.Round(float64(failing) / float64(total) * float64(weight)))
	return clamp(p, 0, weight)
}

// severityFromFraction escalates to critical once at least a third of nodes are
// affected, otherwise warning.
func severityFromFraction(failing, total int) Severity {
	if total == 0 {
		return SeverityInfo
	}
	if float64(failing)/float64(total) >= 1.0/3.0 {
		return SeverityCritical
	}
	return SeverityWarning
}

func statusForScore(score int) Status {
	switch {
	case score >= healthyThreshold:
		return StatusHealthy
	case score >= degradedThreshold:
		return StatusDegraded
	default:
		return StatusCritical
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
