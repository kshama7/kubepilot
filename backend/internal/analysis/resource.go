package analysis

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// ResourceIssueType identifies a category of resource-spec or rightsizing
// problem found on a container or pod.
type ResourceIssueType string

const (
	IssueMissingCPURequest     ResourceIssueType = "MissingCPURequest"
	IssueMissingMemoryRequest  ResourceIssueType = "MissingMemoryRequest"
	IssueMissingMemoryLimit    ResourceIssueType = "MissingMemoryLimit"
	IssueBestEffortQoS         ResourceIssueType = "BestEffortQoS"
	IssueHighLimitRatio        ResourceIssueType = "HighLimitToRequestRatio"
	IssueCPUOverProvisioned    ResourceIssueType = "CPUOverProvisioned"
	IssueMemoryOverProvisioned ResourceIssueType = "MemoryOverProvisioned"
)

// Resource rule thresholds.
const (
	// overProvisionUsageRatio: a container whose live usage is below this
	// fraction of its request is a rightsizing candidate.
	overProvisionUsageRatio = 0.30
	// rightsizeHeadroom is added on top of observed usage when suggesting a new
	// request, so the recommendation leaves burst room.
	rightsizeHeadroom = 0.20
	// highLimitToRequestRatio flags limits set far above requests (noisy-neighbor
	// / QoS eviction risk).
	highLimitToRequestRatio = 4.0
	// Rightsizing is skipped below these request floors — the absolute savings on
	// tiny containers are not worth the churn.
	minRightsizeCPUMilli = 50
	minRightsizeMemBytes = 64 << 20 // 64 MiB
)

// ResourceQuantities holds an optional CPU (millicores) and memory (bytes) pair.
// The *Set flags distinguish "explicitly zero" from "unset".
type ResourceQuantities struct {
	CPUMilli int64 `json:"cpuMilli"`
	MemBytes int64 `json:"memBytes"`
	CPUSet   bool  `json:"cpuSet"`
	MemSet   bool  `json:"memSet"`
}

// ContainerResources is the resource-relevant view of a single container.
type ContainerResources struct {
	Name          string             `json:"name"`
	Init          bool               `json:"init,omitempty"`
	Requests      ResourceQuantities `json:"requests"`
	Limits        ResourceQuantities `json:"limits"`
	UsageCPUMilli int64              `json:"usageCpuMilli,omitempty"`
	UsageMemBytes int64              `json:"usageMemBytes,omitempty"`
	HasUsage      bool               `json:"hasUsage"`
}

// PodResources is the resource-relevant view of a single pod.
type PodResources struct {
	Namespace  string               `json:"namespace"`
	Name       string               `json:"name"`
	QOSClass   string               `json:"qosClass"`
	Containers []ContainerResources `json:"containers"`
}

// ResourceSnapshot is the raw observed state the resource rules evaluate.
type ResourceSnapshot struct {
	ClusterID          string         `json:"clusterId"`
	Namespace          string         `json:"namespace"`
	APIServerReachable bool           `json:"apiServerReachable"`
	APIServerError     string         `json:"apiServerError,omitempty"`
	MetricsAvailable   bool           `json:"metricsAvailable"`
	Pods               []PodResources `json:"pods"`
	CollectedAt        time.Time      `json:"collectedAt"`
}

// ResourceFinding is a single deterministic resource issue or recommendation.
type ResourceFinding struct {
	Type      ResourceIssueType `json:"type"`
	Severity  Severity          `json:"severity"`
	Namespace string            `json:"namespace"`
	Pod       string            `json:"pod"`
	Container string            `json:"container,omitempty"`
	Message   string            `json:"message"`
	Details   map[string]any    `json:"details,omitempty"`
}

// ResourceSummary is a quick-glance rollup, including reclaimable capacity the
// rightsizing rules identified.
type ResourceSummary struct {
	TotalPods              int                       `json:"totalPods"`
	TotalContainers        int                       `json:"totalContainers"`
	ContainersWithIssues   int                       `json:"containersWithIssues"`
	FindingsBySeverity     map[Severity]int          `json:"findingsBySeverity"`
	FindingsByType         map[ResourceIssueType]int `json:"findingsByType"`
	ReclaimableCPUMilli    int64                     `json:"reclaimableCpuMilli"`
	ReclaimableMemoryBytes int64                     `json:"reclaimableMemoryBytes"`
	MetricsAvailable       bool                      `json:"metricsAvailable"`
}

// ResourceReport is the full result of a resource-optimization run.
type ResourceReport struct {
	ClusterID   string            `json:"clusterId"`
	Namespace   string            `json:"namespace"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Summary     ResourceSummary   `json:"summary"`
	Findings    []ResourceFinding `json:"findings"`
}

// AnalyzeResources evaluates the deterministic resource rule set over a
// snapshot. Spec-level checks (missing requests/limits, QoS, limit ratios) run
// always; usage-based rightsizing runs only when MetricsAvailable is true. It
// performs no I/O.
func AnalyzeResources(snap ResourceSnapshot) ResourceReport {
	findings := make([]ResourceFinding, 0)
	var reclaimCPU, reclaimMem int64
	containers := 0
	containersWithIssues := map[string]bool{}

	for _, pod := range snap.Pods {
		// BestEffort means no container declares any request/limit. Emit one
		// pod-level finding rather than a storm of per-container "missing"
		// findings that all say the same thing.
		if pod.QOSClass == "BestEffort" {
			findings = append(findings, ResourceFinding{
				Type: IssueBestEffortQoS, Severity: SeverityWarning,
				Namespace: pod.Namespace, Pod: pod.Name,
				Message: "pod is BestEffort: no resource requests or limits set; first to be evicted under pressure",
			})
			containers += len(pod.Containers)
			continue
		}

		for _, c := range pod.Containers {
			containers++
			fs := evaluateContainerResources(pod, c, snap.MetricsAvailable)
			for _, f := range fs {
				findings = append(findings, f)
				containersWithIssues[pod.Namespace+"/"+pod.Name+"/"+c.Name] = true
				switch f.Type {
				case IssueCPUOverProvisioned:
					if v, ok := f.Details["reclaimableCpuMilli"].(int64); ok {
						reclaimCPU += v
					}
				case IssueMemoryOverProvisioned:
					if v, ok := f.Details["reclaimableMemoryBytes"].(int64); ok {
						reclaimMem += v
					}
				}
			}
		}
	}

	sortResourceFindings(findings)

	return ResourceReport{
		ClusterID:   snap.ClusterID,
		Namespace:   snap.Namespace,
		GeneratedAt: time.Now().UTC(),
		Findings:    findings,
		Summary: ResourceSummary{
			TotalPods:              len(snap.Pods),
			TotalContainers:        containers,
			ContainersWithIssues:   len(containersWithIssues),
			FindingsBySeverity:     countBySeverity(findings),
			FindingsByType:         countByType(findings),
			ReclaimableCPUMilli:    reclaimCPU,
			ReclaimableMemoryBytes: reclaimMem,
			MetricsAvailable:       snap.MetricsAvailable,
		},
	}
}

func evaluateContainerResources(pod PodResources, c ContainerResources, metricsAvailable bool) []ResourceFinding {
	var out []ResourceFinding

	// Missing CPU request hurts bin-packing; missing memory request hurts both
	// scheduling and eviction ordering.
	if !c.Requests.CPUSet {
		out = append(out, resourceFinding(pod, c, IssueMissingCPURequest, SeverityWarning,
			"container has no CPU request; the scheduler cannot bin-pack it reliably", nil))
	}
	if !c.Requests.MemSet {
		out = append(out, resourceFinding(pod, c, IssueMissingMemoryRequest, SeverityWarning,
			"container has no memory request; eviction ordering will treat it as lowest priority", nil))
	}
	// A missing memory limit lets a container consume the node and trigger
	// node-level OOM. (A missing CPU limit is acceptable and often preferred, so
	// it is intentionally not flagged.)
	if !c.Limits.MemSet {
		out = append(out, resourceFinding(pod, c, IssueMissingMemoryLimit, SeverityWarning,
			"container has no memory limit; it can exhaust node memory and trigger OOM", nil))
	}

	// Limit set far above request: bursty workload that can squeeze neighbors and
	// risks eviction when the node fills.
	if c.Requests.CPUSet && c.Limits.CPUSet && c.Requests.CPUMilli > 0 {
		if ratio := float64(c.Limits.CPUMilli) / float64(c.Requests.CPUMilli); ratio >= highLimitToRequestRatio {
			out = append(out, resourceFinding(pod, c, IssueHighLimitRatio, SeverityInfo,
				fmt.Sprintf("CPU limit is %.1fx the request; bursty workload may cause noisy-neighbor contention", ratio),
				map[string]any{"resource": "cpu", "ratio": round1(ratio)}))
		}
	}

	// Usage-based rightsizing — only when metrics-server gave us live usage.
	if metricsAvailable && c.HasUsage {
		out = append(out, rightsizeFindings(pod, c)...)
	}
	return out
}

func rightsizeFindings(pod PodResources, c ContainerResources) []ResourceFinding {
	var out []ResourceFinding

	if c.Requests.CPUSet && c.Requests.CPUMilli >= minRightsizeCPUMilli && c.UsageCPUMilli >= 0 {
		if float64(c.UsageCPUMilli) < overProvisionUsageRatio*float64(c.Requests.CPUMilli) {
			suggested := int64(math.Ceil(float64(c.UsageCPUMilli) * (1 + rightsizeHeadroom)))
			if suggested < c.Requests.CPUMilli {
				out = append(out, resourceFinding(pod, c, IssueCPUOverProvisioned, SeverityInfo,
					fmt.Sprintf("CPU request %dm but live usage %dm; consider ~%dm", c.Requests.CPUMilli, c.UsageCPUMilli, suggested),
					map[string]any{
						"resource":            "cpu",
						"currentRequestMilli": c.Requests.CPUMilli,
						"usageMilli":          c.UsageCPUMilli,
						"suggestedMilli":      suggested,
						"reclaimableCpuMilli": c.Requests.CPUMilli - suggested,
						"basis":               "point-in-time usage; validate against historical peak before applying",
					}))
			}
		}
	}

	if c.Requests.MemSet && c.Requests.MemBytes >= minRightsizeMemBytes && c.UsageMemBytes >= 0 {
		if float64(c.UsageMemBytes) < overProvisionUsageRatio*float64(c.Requests.MemBytes) {
			suggested := int64(math.Ceil(float64(c.UsageMemBytes) * (1 + rightsizeHeadroom)))
			if suggested < c.Requests.MemBytes {
				out = append(out, resourceFinding(pod, c, IssueMemoryOverProvisioned, SeverityInfo,
					fmt.Sprintf("memory request %dMi but live usage %dMi; consider ~%dMi",
						toMi(c.Requests.MemBytes), toMi(c.UsageMemBytes), toMi(suggested)),
					map[string]any{
						"resource":               "memory",
						"currentRequestBytes":    c.Requests.MemBytes,
						"usageBytes":             c.UsageMemBytes,
						"suggestedBytes":         suggested,
						"reclaimableMemoryBytes": c.Requests.MemBytes - suggested,
						"basis":                  "point-in-time usage; validate against historical peak before applying",
					}))
			}
		}
	}
	return out
}

func resourceFinding(pod PodResources, c ContainerResources, t ResourceIssueType, sev Severity, msg string, details map[string]any) ResourceFinding {
	return ResourceFinding{
		Type: t, Severity: sev,
		Namespace: pod.Namespace, Pod: pod.Name, Container: c.Name,
		Message: msg, Details: details,
	}
}

func sortResourceFindings(f []ResourceFinding) {
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

func countBySeverity(f []ResourceFinding) map[Severity]int {
	m := map[Severity]int{}
	for _, x := range f {
		m[x.Severity]++
	}
	return m
}

func countByType(f []ResourceFinding) map[ResourceIssueType]int {
	m := map[ResourceIssueType]int{}
	for _, x := range f {
		m[x.Type]++
	}
	return m
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func toMi(bytes int64) int64 { return bytes / (1 << 20) }
