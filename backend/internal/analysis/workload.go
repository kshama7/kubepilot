package analysis

import (
	"fmt"
	"sort"
	"time"
)

// WorkloadIssueType identifies a category of pod-level problem. Each answers a
// distinct on-call question, so a single container can surface more than one
// (e.g. an OOMKilled container that is now CrashLoopBackOff and churning
// restarts — three different signals an SRE wants to see).
type WorkloadIssueType string

const (
	IssueCrashLoopBackOff WorkloadIssueType = "CrashLoopBackOff"
	IssueOOMKilled        WorkloadIssueType = "OOMKilled"
	IssueImagePull        WorkloadIssueType = "ImagePullError"
	IssueContainerError   WorkloadIssueType = "ContainerError"
	IssueUnschedulable    WorkloadIssueType = "Unschedulable"
	IssuePendingStuck     WorkloadIssueType = "PendingStuck"
	IssueRestartStorm     WorkloadIssueType = "RestartStorm"
	IssueNotReady         WorkloadIssueType = "NotReady"
	IssueFailed           WorkloadIssueType = "Failed"
	IssueUnknownPhase     WorkloadIssueType = "UnknownPhase"
)

// Tunable rule thresholds. Chosen to match what actually pages someone rather
// than what merely looks busy on a dashboard.
const (
	restartStormThreshold         = 5               // restarts before we flag churn
	restartStormCriticalThreshold = 20              // restarts that escalate to critical
	pendingGracePeriod            = 5 * time.Minute // how long Pending is tolerated before "stuck"
)

// imagePullReasons are container waiting reasons that mean the image could not
// be pulled or resolved.
var imagePullReasons = map[string]bool{
	"ImagePullBackOff":    true,
	"ErrImagePull":        true,
	"InvalidImageName":    true,
	"ImageInspectError":   true,
	"RegistryUnavailable": true,
}

// containerErrorReasons are waiting reasons that indicate the kubelet could not
// create or start the container (config/runtime problems, not image pulls).
var containerErrorReasons = map[string]bool{
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"RunContainerError":          true,
	"CreatePodSandboxError":      true,
}

// ContainerState is the scoring-relevant view of a single container's status.
type ContainerState struct {
	Name                   string `json:"name"`
	Ready                  bool   `json:"ready"`
	Started                bool   `json:"started"`
	RestartCount           int32  `json:"restartCount"`
	WaitingReason          string `json:"waitingReason,omitempty"`
	WaitingMessage         string `json:"waitingMessage,omitempty"`
	LastTerminatedReason   string `json:"lastTerminatedReason,omitempty"`
	LastTerminatedExitCode int32  `json:"lastTerminatedExitCode,omitempty"`
	Init                   bool   `json:"init,omitempty"`
}

// PodStatus is the distilled, scoring-relevant view of a single pod.
type PodStatus struct {
	Namespace            string           `json:"namespace"`
	Name                 string           `json:"name"`
	Phase                string           `json:"phase"`
	NodeName             string           `json:"nodeName,omitempty"`
	Scheduled            bool             `json:"scheduled"`
	UnschedulableReason  string           `json:"unschedulableReason,omitempty"`
	UnschedulableMessage string           `json:"unschedulableMessage,omitempty"`
	CreatedAt            time.Time        `json:"createdAt"`
	Containers           []ContainerState `json:"containers"`
}

// WorkloadSnapshot is the raw observed state the workload rules evaluate.
type WorkloadSnapshot struct {
	ClusterID          string      `json:"clusterId"`
	Namespace          string      `json:"namespace"` // "" means all namespaces
	APIServerReachable bool        `json:"apiServerReachable"`
	APIServerError     string      `json:"apiServerError,omitempty"`
	Pods               []PodStatus `json:"pods"`
	CollectedAt        time.Time   `json:"collectedAt"`
}

// WorkloadFinding is a single deterministic issue tied to a pod (and optionally
// a container).
type WorkloadFinding struct {
	Type      WorkloadIssueType `json:"type"`
	Severity  Severity          `json:"severity"`
	Namespace string            `json:"namespace"`
	Pod       string            `json:"pod"`
	Container string            `json:"container,omitempty"`
	Message   string            `json:"message"`
	Details   map[string]any    `json:"details,omitempty"`
}

// WorkloadSummary is a quick-glance rollup for the dashboard.
type WorkloadSummary struct {
	TotalPods          int                       `json:"totalPods"`
	HealthyPods        int                       `json:"healthyPods"`
	PodsWithIssues     int                       `json:"podsWithIssues"`
	FindingsBySeverity map[Severity]int          `json:"findingsBySeverity"`
	FindingsByType     map[WorkloadIssueType]int `json:"findingsByType"`
}

// WorkloadReport is the full result of a workload-analysis run.
type WorkloadReport struct {
	ClusterID   string            `json:"clusterId"`
	Namespace   string            `json:"namespace"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Summary     WorkloadSummary   `json:"summary"`
	Findings    []WorkloadFinding `json:"findings"`
}

// AnalyzeWorkloads evaluates the deterministic workload rule set over a
// snapshot. It performs no I/O and is the unit-tested core of module 2.
func AnalyzeWorkloads(snap WorkloadSnapshot) WorkloadReport {
	findings := make([]WorkloadFinding, 0)
	podsWithIssues := 0

	for _, pod := range snap.Pods {
		before := len(findings)
		findings = append(findings, evaluatePod(pod, snap.CollectedAt)...)
		if len(findings) > before {
			podsWithIssues++
		}
	}

	sortFindings(findings)

	report := WorkloadReport{
		ClusterID:   snap.ClusterID,
		Namespace:   snap.Namespace,
		GeneratedAt: time.Now().UTC(),
		Findings:    findings,
		Summary:     summarizeWorkloads(snap, findings, podsWithIssues),
	}
	return report
}

// evaluatePod runs every applicable rule against one pod, emitting independent
// findings for each distinct problem.
func evaluatePod(pod PodStatus, now time.Time) []WorkloadFinding {
	var out []WorkloadFinding

	switch pod.Phase {
	case "Failed":
		out = append(out, WorkloadFinding{
			Type: IssueFailed, Severity: SeverityCritical,
			Namespace: pod.Namespace, Pod: pod.Name,
			Message: "pod is in the Failed phase",
		})
	case "Unknown":
		out = append(out, WorkloadFinding{
			Type: IssueUnknownPhase, Severity: SeverityWarning,
			Namespace: pod.Namespace, Pod: pod.Name,
			Message: "pod phase is Unknown; node may be unreachable",
		})
	case "Pending":
		out = append(out, pendingFindings(pod, now)...)
	}

	for _, c := range pod.Containers {
		out = append(out, containerFindings(pod, c)...)
	}
	return out
}

func pendingFindings(pod PodStatus, now time.Time) []WorkloadFinding {
	if !pod.Scheduled && pod.UnschedulableReason != "" {
		return []WorkloadFinding{{
			Type: IssueUnschedulable, Severity: SeverityCritical,
			Namespace: pod.Namespace, Pod: pod.Name,
			Message: "pod cannot be scheduled onto any node",
			Details: pruneEmpty(map[string]any{
				"reason":  pod.UnschedulableReason,
				"message": pod.UnschedulableMessage,
			}),
		}}
	}

	// Tolerate freshly-created pods still pulling images / creating sandboxes.
	age := now.Sub(pod.CreatedAt)
	if age < pendingGracePeriod {
		return nil
	}
	return []WorkloadFinding{{
		Type: IssuePendingStuck, Severity: SeverityWarning,
		Namespace: pod.Namespace, Pod: pod.Name,
		Message: fmt.Sprintf("pod has been Pending for %s", age.Round(time.Second)),
		Details: map[string]any{"pendingForSeconds": int(age.Seconds())},
	}}
}

func containerFindings(pod PodStatus, c ContainerState) []WorkloadFinding {
	var out []WorkloadFinding

	switch {
	case c.WaitingReason == "CrashLoopBackOff":
		out = append(out, containerFinding(pod, c, IssueCrashLoopBackOff, SeverityCritical,
			"container is in CrashLoopBackOff"))
	case imagePullReasons[c.WaitingReason]:
		out = append(out, containerFinding(pod, c, IssueImagePull, SeverityCritical,
			"container image cannot be pulled: "+c.WaitingReason))
	case containerErrorReasons[c.WaitingReason]:
		out = append(out, containerFinding(pod, c, IssueContainerError, SeverityCritical,
			"container failed to start: "+c.WaitingReason))
	}

	// OOMKilled is independent of the current waiting state — the container may
	// have been killed for memory and then re-entered CrashLoopBackOff.
	if c.LastTerminatedReason == "OOMKilled" {
		out = append(out, containerFinding(pod, c, IssueOOMKilled, SeverityCritical,
			"container was OOMKilled; raise the memory limit or fix the leak"))
	}

	// Restart storm: high churn regardless of current state.
	if c.RestartCount >= restartStormThreshold {
		sev := SeverityWarning
		if c.RestartCount >= restartStormCriticalThreshold {
			sev = SeverityCritical
		}
		out = append(out, containerFinding(pod, c, IssueRestartStorm, sev,
			fmt.Sprintf("container has restarted %d times", c.RestartCount)))
	}

	// Readiness: a started-but-not-ready container in a Running pod almost always
	// means a failing readiness probe. Skip if it's already crashlooping (the
	// crashloop finding is the real story there).
	if pod.Phase == "Running" && c.Started && !c.Ready && c.WaitingReason == "" {
		out = append(out, containerFinding(pod, c, IssueNotReady, SeverityWarning,
			"container is running but not Ready; readiness probe is likely failing"))
	}

	return out
}

func containerFinding(pod PodStatus, c ContainerState, t WorkloadIssueType, sev Severity, msg string) WorkloadFinding {
	return WorkloadFinding{
		Type: t, Severity: sev,
		Namespace: pod.Namespace, Pod: pod.Name, Container: c.Name,
		Message: msg,
		Details: pruneEmpty(map[string]any{
			"restartCount":         int(c.RestartCount),
			"waitingReason":        c.WaitingReason,
			"waitingMessage":       c.WaitingMessage,
			"lastTerminatedReason": c.LastTerminatedReason,
		}),
	}
}

func summarizeWorkloads(snap WorkloadSnapshot, findings []WorkloadFinding, podsWithIssues int) WorkloadSummary {
	s := WorkloadSummary{
		TotalPods:          len(snap.Pods),
		PodsWithIssues:     podsWithIssues,
		HealthyPods:        len(snap.Pods) - podsWithIssues,
		FindingsBySeverity: map[Severity]int{},
		FindingsByType:     map[WorkloadIssueType]int{},
	}
	for _, f := range findings {
		s.FindingsBySeverity[f.Severity]++
		s.FindingsByType[f.Type]++
	}
	return s
}

// sortFindings orders findings critical-first, then by namespace, pod, type for
// stable, scannable output.
func sortFindings(f []WorkloadFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if r := severityRank(a.Severity) - severityRank(b.Severity); r != 0 {
			return r < 0
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Pod != b.Pod {
			return a.Pod < b.Pod
		}
		return a.Type < b.Type
	})
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarning:
		return 1
	default:
		return 2
	}
}

// pruneEmpty drops nil/empty/zero values so finding details stay terse.
func pruneEmpty(m map[string]any) map[string]any {
	for k, v := range m {
		switch val := v.(type) {
		case string:
			if val == "" {
				delete(m, k)
			}
		case int:
			if val == 0 {
				delete(m, k)
			}
		case nil:
			delete(m, k)
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
