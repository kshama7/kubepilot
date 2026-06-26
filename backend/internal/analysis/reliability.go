package analysis

import (
	"sort"
	"time"
)

// ReliabilityIssueType identifies a category of workload reliability gap.
type ReliabilityIssueType string

const (
	IssueSingleReplica         ReliabilityIssueType = "SingleReplica"
	IssueMissingPDB            ReliabilityIssueType = "MissingPodDisruptionBudget"
	IssuePDBBlocksDrain        ReliabilityIssueType = "PDBBlocksVoluntaryDisruption"
	IssueNoSpreadConstraints   ReliabilityIssueType = "NoSpreadConstraints"
	IssueMissingReadinessProbe ReliabilityIssueType = "MissingReadinessProbe"
	IssueMissingLivenessProbe  ReliabilityIssueType = "MissingLivenessProbe"
)

// ContainerProbes records which probes a container declares.
type ContainerProbes struct {
	Name         string `json:"name"`
	Init         bool   `json:"init,omitempty"`
	HasReadiness bool   `json:"hasReadiness"`
	HasLiveness  bool   `json:"hasLiveness"`
	HasStartup   bool   `json:"hasStartup,omitempty"`
}

// PDBRef is a PodDisruptionBudget that selects a workload's pods. AllowsDisruption
// is resolved by the collector against the workload's replica count using real
// intstr/percent semantics, so the rule layer stays free of apimachinery.
type PDBRef struct {
	Name             string `json:"name"`
	MinAvailable     string `json:"minAvailable,omitempty"`
	MaxUnavailable   string `json:"maxUnavailable,omitempty"`
	AllowsDisruption bool   `json:"allowsDisruption"`
}

// WorkloadSpec is the reliability-relevant view of a Deployment or StatefulSet.
type WorkloadSpec struct {
	Namespace          string            `json:"namespace"`
	Name               string            `json:"name"`
	Kind               string            `json:"kind"`
	Replicas           int32             `json:"replicas"`
	Containers         []ContainerProbes `json:"containers"`
	HasPodAntiAffinity bool              `json:"hasPodAntiAffinity"`
	HasTopologySpread  bool              `json:"hasTopologySpread"`
	PDBs               []PDBRef          `json:"pdbs"`
}

// ReliabilitySnapshot is the raw observed state the reliability rules evaluate.
type ReliabilitySnapshot struct {
	ClusterID          string         `json:"clusterId"`
	Namespace          string         `json:"namespace"`
	APIServerReachable bool           `json:"apiServerReachable"`
	APIServerError     string         `json:"apiServerError,omitempty"`
	Workloads          []WorkloadSpec `json:"workloads"`
	CollectedAt        time.Time      `json:"collectedAt"`
}

// ReliabilityFinding is a single deterministic reliability gap on a workload.
type ReliabilityFinding struct {
	Type      ReliabilityIssueType `json:"type"`
	Severity  Severity             `json:"severity"`
	Namespace string               `json:"namespace"`
	Workload  string               `json:"workload"`
	Kind      string               `json:"kind"`
	Container string               `json:"container,omitempty"`
	Message   string               `json:"message"`
	Details   map[string]any       `json:"details,omitempty"`
}

// ReliabilitySummary is a quick-glance rollup with coverage counters.
type ReliabilitySummary struct {
	TotalWorkloads         int                          `json:"totalWorkloads"`
	WorkloadsWithIssues    int                          `json:"workloadsWithIssues"`
	MultiReplicaWorkloads  int                          `json:"multiReplicaWorkloads"`
	MultiReplicaWithoutPDB int                          `json:"multiReplicaWithoutPdb"`
	FindingsBySeverity     map[Severity]int             `json:"findingsBySeverity"`
	FindingsByType         map[ReliabilityIssueType]int `json:"findingsByType"`
}

// ReliabilityReport is the full result of a reliability-analysis run.
type ReliabilityReport struct {
	ClusterID   string               `json:"clusterId"`
	Namespace   string               `json:"namespace"`
	GeneratedAt time.Time            `json:"generatedAt"`
	Summary     ReliabilitySummary   `json:"summary"`
	Findings    []ReliabilityFinding `json:"findings"`
}

// AnalyzeReliability evaluates the deterministic reliability rule set over a
// snapshot. It performs no I/O.
func AnalyzeReliability(snap ReliabilitySnapshot) ReliabilityReport {
	findings := make([]ReliabilityFinding, 0)
	workloadsWithIssues := 0
	multiReplica := 0
	multiReplicaNoPDB := 0

	for _, w := range snap.Workloads {
		if w.Replicas > 1 {
			multiReplica++
			if len(w.PDBs) == 0 {
				multiReplicaNoPDB++
			}
		}
		before := len(findings)
		findings = append(findings, evaluateWorkload(w)...)
		if len(findings) > before {
			workloadsWithIssues++
		}
	}

	sortReliabilityFindings(findings)

	return ReliabilityReport{
		ClusterID:   snap.ClusterID,
		Namespace:   snap.Namespace,
		GeneratedAt: time.Now().UTC(),
		Findings:    findings,
		Summary: ReliabilitySummary{
			TotalWorkloads:         len(snap.Workloads),
			WorkloadsWithIssues:    workloadsWithIssues,
			MultiReplicaWorkloads:  multiReplica,
			MultiReplicaWithoutPDB: multiReplicaNoPDB,
			FindingsBySeverity:     reliabilityCountBySeverity(findings),
			FindingsByType:         reliabilityCountByType(findings),
		},
	}
}

func evaluateWorkload(w WorkloadSpec) []ReliabilityFinding {
	var out []ReliabilityFinding

	switch {
	case w.Replicas == 1:
		// A single replica is a single point of failure: any node drain, crash,
		// or rollout briefly takes the whole service down.
		out = append(out, reliabilityFinding(w, "", IssueSingleReplica, SeverityWarning,
			"workload runs a single replica; it is a single point of failure during drains and rollouts", nil))

	case w.Replicas > 1:
		if len(w.PDBs) == 0 {
			out = append(out, reliabilityFinding(w, "", IssueMissingPDB, SeverityWarning,
				"multi-replica workload has no PodDisruptionBudget; a node drain can evict all replicas at once", nil))
		}
		for _, p := range w.PDBs {
			if !p.AllowsDisruption {
				out = append(out, reliabilityFinding(w, "", IssuePDBBlocksDrain, SeverityWarning,
					"PodDisruptionBudget permits zero voluntary disruptions; node drains will block indefinitely",
					pruneEmpty(map[string]any{
						"pdb":            p.Name,
						"minAvailable":   p.MinAvailable,
						"maxUnavailable": p.MaxUnavailable,
					})))
			}
		}
		// With more than one replica but no spread policy, the scheduler may stack
		// every replica on one node, defeating the redundancy.
		if !w.HasPodAntiAffinity && !w.HasTopologySpread {
			out = append(out, reliabilityFinding(w, "", IssueNoSpreadConstraints, SeverityWarning,
				"multi-replica workload has neither pod anti-affinity nor topology spread; replicas may co-locate on one node", nil))
		}
	}

	for _, c := range w.Containers {
		if c.Init {
			continue // init containers run to completion; probes do not apply
		}
		if !c.HasReadiness {
			out = append(out, reliabilityFinding(w, c.Name, IssueMissingReadinessProbe, SeverityWarning,
				"container has no readiness probe; it receives traffic before it is ready", nil))
		}
		if !c.HasLiveness {
			out = append(out, reliabilityFinding(w, c.Name, IssueMissingLivenessProbe, SeverityInfo,
				"container has no liveness probe; a wedged process will not be restarted automatically", nil))
		}
	}
	return out
}

func reliabilityFinding(w WorkloadSpec, container string, t ReliabilityIssueType, sev Severity, msg string, details map[string]any) ReliabilityFinding {
	return ReliabilityFinding{
		Type: t, Severity: sev,
		Namespace: w.Namespace, Workload: w.Name, Kind: w.Kind, Container: container,
		Message: msg, Details: details,
	}
}

func sortReliabilityFindings(f []ReliabilityFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if r := severityRank(a.Severity) - severityRank(b.Severity); r != 0 {
			return r < 0
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Workload != b.Workload {
			return a.Workload < b.Workload
		}
		return a.Type < b.Type
	})
}

func reliabilityCountBySeverity(f []ReliabilityFinding) map[Severity]int {
	m := map[Severity]int{}
	for _, x := range f {
		m[x.Severity]++
	}
	return m
}

func reliabilityCountByType(f []ReliabilityFinding) map[ReliabilityIssueType]int {
	m := map[ReliabilityIssueType]int{}
	for _, x := range f {
		m[x.Type]++
	}
	return m
}
