package analysis

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// CapacityIssueType identifies a category of capacity/saturation problem.
type CapacityIssueType string

const (
	IssueHighCPUUtilization        CapacityIssueType = "HighCPUUtilization"
	IssueHighMemoryUtilization     CapacityIssueType = "HighMemoryUtilization"
	IssueCPUSaturationPredicted    CapacityIssueType = "CPUSaturationPredicted"
	IssueMemorySaturationPredicted CapacityIssueType = "MemorySaturationPredicted"
	IssueHighPodDensity            CapacityIssueType = "HighPodDensity"
	IssueHighCPUCommitment         CapacityIssueType = "HighCPUCommitment"
	IssueHighMemoryCommitment      CapacityIssueType = "HighMemoryCommitment"
)

// Capacity rule thresholds.
const (
	utilWarn            = 0.85 // current utilization flagged as a warning
	utilCritical        = 0.95 // current utilization flagged as critical
	saturationThreshold = 0.90 // the level we predict time-to-reach
	commitmentWarn      = 0.90 // requested/allocatable flagged as a warning
	commitmentCritical  = 1.00 // fully committed: no schedulable headroom
	podDensityWarn      = 0.90 // pods/maxPods flagged as a warning
	saturationHorizon   = 7.0  // predict only within this many days
	saturationCritDays  = 2.0  // predicted saturation sooner than this is critical
)

// Sample is one (timestamp, value) point in a utilization series. Value is a
// fraction in [0,1].
type Sample struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"value"`
}

// NodeCapacity is the capacity-relevant view of one node. Capacity, allocatable,
// density, and commitment come from the Kubernetes API; the utilization series
// come from Prometheus when available.
type NodeCapacity struct {
	Name                string   `json:"name"`
	AllocatableCPUMilli int64    `json:"allocatableCpuMilli"`
	AllocatableMemBytes int64    `json:"allocatableMemBytes"`
	MaxPods             int      `json:"maxPods"`
	PodCount            int      `json:"podCount"`
	RequestedCPUMilli   int64    `json:"requestedCpuMilli"`
	RequestedMemBytes   int64    `json:"requestedMemBytes"`
	HasUtilization      bool     `json:"hasUtilization"`
	CPUUtilSeries       []Sample `json:"cpuUtilSeries,omitempty"`
	MemUtilSeries       []Sample `json:"memUtilSeries,omitempty"`
}

// CapacitySnapshot is the raw observed state the capacity rules evaluate.
type CapacitySnapshot struct {
	ClusterID           string         `json:"clusterId"`
	APIServerReachable  bool           `json:"apiServerReachable"`
	APIServerError      string         `json:"apiServerError,omitempty"`
	PrometheusAvailable bool           `json:"prometheusAvailable"`
	LookbackHours       float64        `json:"lookbackHours"`
	Nodes               []NodeCapacity `json:"nodes"`
	CollectedAt         time.Time      `json:"collectedAt"`
}

// CapacityFinding is a single deterministic capacity issue or prediction.
type CapacityFinding struct {
	Type     CapacityIssueType `json:"type"`
	Severity Severity          `json:"severity"`
	Node     string            `json:"node"`
	Message  string            `json:"message"`
	Details  map[string]any    `json:"details,omitempty"`
}

// CapacitySummary is a quick-glance fleet rollup.
type CapacitySummary struct {
	TotalNodes             int                       `json:"totalNodes"`
	NodesNearSaturation    int                       `json:"nodesNearSaturation"`
	PrometheusAvailable    bool                      `json:"prometheusAvailable"`
	MinDaysToCPUSaturation float64                   `json:"minDaysToCpuSaturation"` // -1 if none predicted
	MinDaysToMemSaturation float64                   `json:"minDaysToMemSaturation"`
	FindingsBySeverity     map[Severity]int          `json:"findingsBySeverity"`
	FindingsByType         map[CapacityIssueType]int `json:"findingsByType"`
}

// CapacityReport is the full result of a capacity-planning run.
type CapacityReport struct {
	ClusterID   string            `json:"clusterId"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Summary     CapacitySummary   `json:"summary"`
	Findings    []CapacityFinding `json:"findings"`
}

// AnalyzeCapacity evaluates the deterministic capacity rule set over a snapshot.
// It performs no I/O. Density and commitment are always evaluated; utilization
// and saturation prediction run per node only when a utilization series exists.
func AnalyzeCapacity(snap CapacitySnapshot) CapacityReport {
	findings := make([]CapacityFinding, 0)
	nodesNearSaturation := 0
	minCPUDays, minMemDays := -1.0, -1.0

	for _, n := range snap.Nodes {
		findings = append(findings, densityAndCommitment(n)...)

		if !n.HasUtilization {
			continue
		}
		near := false

		cpuFindings, cpuDays := utilizationFindings(n, n.CPUUtilSeries, "cpu",
			IssueHighCPUUtilization, IssueCPUSaturationPredicted)
		findings = append(findings, cpuFindings...)
		if currentUtil(n.CPUUtilSeries) >= utilWarn {
			near = true
		}
		minCPUDays = minPositive(minCPUDays, cpuDays)

		memFindings, memDays := utilizationFindings(n, n.MemUtilSeries, "memory",
			IssueHighMemoryUtilization, IssueMemorySaturationPredicted)
		findings = append(findings, memFindings...)
		if currentUtil(n.MemUtilSeries) >= utilWarn {
			near = true
		}
		minMemDays = minPositive(minMemDays, memDays)

		if near {
			nodesNearSaturation++
		}
	}

	sortCapacityFindings(findings)

	return CapacityReport{
		ClusterID:   snap.ClusterID,
		GeneratedAt: time.Now().UTC(),
		Findings:    findings,
		Summary: CapacitySummary{
			TotalNodes:             len(snap.Nodes),
			NodesNearSaturation:    nodesNearSaturation,
			PrometheusAvailable:    snap.PrometheusAvailable,
			MinDaysToCPUSaturation: minCPUDays,
			MinDaysToMemSaturation: minMemDays,
			FindingsBySeverity:     capacityCountBySeverity(findings),
			FindingsByType:         capacityCountByType(findings),
		},
	}
}

func densityAndCommitment(n NodeCapacity) []CapacityFinding {
	var out []CapacityFinding

	if n.MaxPods > 0 {
		density := float64(n.PodCount) / float64(n.MaxPods)
		if density >= podDensityWarn {
			out = append(out, CapacityFinding{
				Type: IssueHighPodDensity, Severity: SeverityWarning, Node: n.Name,
				Message: fmt.Sprintf("node is at %.0f%% pod density (%d/%d)", density*100, n.PodCount, n.MaxPods),
				Details: map[string]any{"podCount": n.PodCount, "maxPods": n.MaxPods, "density": round2(density)},
			})
		}
	}

	if n.AllocatableCPUMilli > 0 {
		commit := float64(n.RequestedCPUMilli) / float64(n.AllocatableCPUMilli)
		if sev, ok := commitmentSeverity(commit); ok {
			out = append(out, CapacityFinding{
				Type: IssueHighCPUCommitment, Severity: sev, Node: n.Name,
				Message: fmt.Sprintf("CPU requests commit %.0f%% of allocatable; little headroom to schedule", commit*100),
				Details: map[string]any{"requestedMilli": n.RequestedCPUMilli, "allocatableMilli": n.AllocatableCPUMilli, "commitment": round2(commit)},
			})
		}
	}

	if n.AllocatableMemBytes > 0 {
		commit := float64(n.RequestedMemBytes) / float64(n.AllocatableMemBytes)
		if sev, ok := commitmentSeverity(commit); ok {
			out = append(out, CapacityFinding{
				Type: IssueHighMemoryCommitment, Severity: sev, Node: n.Name,
				Message: fmt.Sprintf("memory requests commit %.0f%% of allocatable; little headroom to schedule", commit*100),
				Details: map[string]any{"requestedBytes": n.RequestedMemBytes, "allocatableBytes": n.AllocatableMemBytes, "commitment": round2(commit)},
			})
		}
	}
	return out
}

// utilizationFindings emits high-utilization and saturation-prediction findings
// for one resource series and returns the predicted days-to-saturation (-1 when
// not predicted).
func utilizationFindings(n NodeCapacity, series []Sample, resource string, highType, satType CapacityIssueType) ([]CapacityFinding, float64) {
	var out []CapacityFinding
	current := currentUtil(series)

	if current >= utilCritical {
		out = append(out, capFinding(n, highType, SeverityCritical, resource,
			fmt.Sprintf("%s utilization is %.0f%%", resource, current*100), current, nil))
	} else if current >= utilWarn {
		out = append(out, capFinding(n, highType, SeverityWarning, resource,
			fmt.Sprintf("%s utilization is %.0f%%", resource, current*100), current, nil))
	}

	days := -1.0
	if slope, ok := linearSlopePerHour(series); ok && slope > 0 {
		days = daysToThreshold(current, slope, saturationThreshold)
		if days >= 0 && days <= saturationHorizon {
			sev := SeverityWarning
			if days <= saturationCritDays {
				sev = SeverityCritical
			}
			out = append(out, capFinding(n, satType, sev, resource,
				fmt.Sprintf("%s is trending toward %.0f%% in ~%.1f day(s) at the current rate", resource, saturationThreshold*100, days),
				current, map[string]any{
					"predictedDaysToSaturation": round2(days),
					"slopePerHour":              round4(slope),
					"thresholdFraction":         saturationThreshold,
				}))
		}
	}
	return out, days
}

func capFinding(n NodeCapacity, t CapacityIssueType, sev Severity, resource, msg string, current float64, extra map[string]any) CapacityFinding {
	details := map[string]any{"resource": resource, "currentUtilization": round2(current)}
	for k, v := range extra {
		details[k] = v
	}
	return CapacityFinding{Type: t, Severity: sev, Node: n.Name, Message: msg, Details: details}
}

// currentUtil returns the most recent sample value, or 0 for an empty series.
func currentUtil(series []Sample) float64 {
	if len(series) == 0 {
		return 0
	}
	return series[len(series)-1].Value
}

// linearSlopePerHour fits a least-squares line to the series and returns the
// slope in utilization-fraction per hour. ok is false with fewer than two
// distinct-time points or zero time variance.
func linearSlopePerHour(series []Sample) (float64, bool) {
	if len(series) < 2 {
		return 0, false
	}
	t0 := series[0].TS
	var sx, sy, sxx, sxy float64
	n := float64(len(series))
	for _, s := range series {
		x := s.TS.Sub(t0).Hours()
		y := s.Value
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	denom := n*sxx - sx*sx
	if denom == 0 {
		return 0, false
	}
	return (n*sxy - sx*sy) / denom, true
}

// daysToThreshold projects when utilization reaches threshold at a constant
// slope. Returns 0 if already at/above threshold, -1 if not increasing.
func daysToThreshold(current, slopePerHour, threshold float64) float64 {
	if current >= threshold {
		return 0
	}
	if slopePerHour <= 0 {
		return -1
	}
	return (threshold - current) / slopePerHour / 24.0
}

func commitmentSeverity(commit float64) (Severity, bool) {
	switch {
	case commit >= commitmentCritical:
		return SeverityCritical, true
	case commit >= commitmentWarn:
		return SeverityWarning, true
	default:
		return "", false
	}
}

// minPositive returns the smaller of two days-to-saturation values, ignoring the
// -1 "not predicted" sentinel.
func minPositive(cur, next float64) float64 {
	if next < 0 {
		return cur
	}
	if cur < 0 {
		return next
	}
	return math.Min(cur, next)
}

func sortCapacityFindings(f []CapacityFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if r := severityRank(a.Severity) - severityRank(b.Severity); r != 0 {
			return r < 0
		}
		if a.Node != b.Node {
			return a.Node < b.Node
		}
		return a.Type < b.Type
	})
}

func capacityCountBySeverity(f []CapacityFinding) map[Severity]int {
	m := map[Severity]int{}
	for _, x := range f {
		m[x.Severity]++
	}
	return m
}

func capacityCountByType(f []CapacityFinding) map[CapacityIssueType]int {
	m := map[CapacityIssueType]int{}
	for _, x := range f {
		m[x.Type]++
	}
	return m
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
