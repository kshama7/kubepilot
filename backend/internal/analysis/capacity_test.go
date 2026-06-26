package analysis

import (
	"math"
	"testing"
	"time"
)

// series builds a utilization series of evenly-spaced points ending now.
func series(stepMinutes int, values ...float64) []Sample {
	out := make([]Sample, len(values))
	start := time.Now().Add(-time.Duration(stepMinutes*(len(values)-1)) * time.Minute)
	for i, v := range values {
		out[i] = Sample{TS: start.Add(time.Duration(stepMinutes*i) * time.Minute), Value: v}
	}
	return out
}

func capSnap(prom bool, nodes ...NodeCapacity) CapacitySnapshot {
	return CapacitySnapshot{
		ClusterID: "c", APIServerReachable: true, PrometheusAvailable: prom,
		LookbackHours: 6, CollectedAt: time.Now(), Nodes: nodes,
	}
}

func TestLinearSlopePerHour(t *testing.T) {
	// 0.10 increase per hour: samples 60 min apart rising 0.10 each.
	s := series(60, 0.20, 0.30, 0.40, 0.50)
	slope, ok := linearSlopePerHour(s)
	if !ok {
		t.Fatal("expected a slope")
	}
	if math.Abs(slope-0.10) > 1e-6 {
		t.Fatalf("expected slope ~0.10/hr, got %f", slope)
	}
}

func TestLinearSlopePerHour_Flat(t *testing.T) {
	s := series(30, 0.5, 0.5, 0.5, 0.5)
	slope, ok := linearSlopePerHour(s)
	if !ok || math.Abs(slope) > 1e-9 {
		t.Fatalf("flat series should have ~0 slope, got %f ok=%v", slope, ok)
	}
}

func TestDaysToThreshold(t *testing.T) {
	// current 0.50, +0.05/hr → to 0.90 is 8 hours = 0.333 days.
	d := daysToThreshold(0.50, 0.05, 0.90)
	if math.Abs(d-8.0/24.0) > 1e-9 {
		t.Fatalf("expected ~0.333 days, got %f", d)
	}
	if daysToThreshold(0.95, 0.05, 0.90) != 0 {
		t.Fatal("already above threshold should be 0 days")
	}
	if daysToThreshold(0.50, -0.01, 0.90) != -1 {
		t.Fatal("decreasing utilization should be -1 (no saturation)")
	}
}

func TestAnalyzeCapacity_HighCPUUtilization(t *testing.T) {
	n := NodeCapacity{
		Name: "worker-0", AllocatableCPUMilli: 4000, AllocatableMemBytes: 8 << 30,
		MaxPods: 110, PodCount: 10, HasUtilization: true,
		CPUUtilSeries: series(60, 0.96, 0.96, 0.96), // flat & critical
		MemUtilSeries: series(60, 0.20, 0.20, 0.20),
	}
	got := AnalyzeCapacity(capSnap(true, n))
	f := requireCapFinding(t, got, IssueHighCPUUtilization)
	if f.Severity != SeverityCritical {
		t.Fatalf("96%% CPU should be critical, got %q", f.Severity)
	}
	if got.Summary.NodesNearSaturation != 1 {
		t.Fatalf("expected 1 node near saturation, got %d", got.Summary.NodesNearSaturation)
	}
}

func TestAnalyzeCapacity_SaturationPredicted(t *testing.T) {
	// Rising 0.02/hr from 0.70 → reaches 0.90 in 10 hours ≈ 0.42 days (critical).
	n := NodeCapacity{
		Name: "worker-1", AllocatableCPUMilli: 4000, MaxPods: 110, PodCount: 5,
		HasUtilization: true,
		CPUUtilSeries:  series(60, 0.70, 0.72, 0.74, 0.76),
		MemUtilSeries:  series(60, 0.10, 0.10, 0.10, 0.10),
	}
	got := AnalyzeCapacity(capSnap(true, n))
	f := requireCapFinding(t, got, IssueCPUSaturationPredicted)
	if f.Severity != SeverityCritical {
		t.Fatalf("saturation within 2 days should be critical, got %q", f.Severity)
	}
	if got.Summary.MinDaysToCPUSaturation <= 0 {
		t.Fatalf("expected a positive days-to-saturation, got %f", got.Summary.MinDaysToCPUSaturation)
	}
}

func TestAnalyzeCapacity_NoSaturationWhenFlat(t *testing.T) {
	n := NodeCapacity{
		Name: "worker-2", AllocatableCPUMilli: 4000, MaxPods: 110, PodCount: 5,
		HasUtilization: true,
		CPUUtilSeries:  series(60, 0.50, 0.50, 0.50),
		MemUtilSeries:  series(60, 0.50, 0.50, 0.50),
	}
	got := AnalyzeCapacity(capSnap(true, n))
	if hasCapType(got, IssueCPUSaturationPredicted) {
		t.Fatal("flat utilization should not predict saturation")
	}
	if got.Summary.MinDaysToCPUSaturation != -1 {
		t.Fatalf("expected -1 (no prediction), got %f", got.Summary.MinDaysToCPUSaturation)
	}
}

func TestAnalyzeCapacity_DensityAndCommitmentWithoutPrometheus(t *testing.T) {
	n := NodeCapacity{
		Name: "worker-3", AllocatableCPUMilli: 4000, AllocatableMemBytes: 8 << 30,
		MaxPods: 110, PodCount: 105, // 95% density
		RequestedCPUMilli: 4000, // 100% committed → critical
		RequestedMemBytes: 6 << 30,
		HasUtilization:    false, // no Prometheus
	}
	got := AnalyzeCapacity(capSnap(false, n))
	requireCapFinding(t, got, IssueHighPodDensity)
	commit := requireCapFinding(t, got, IssueHighCPUCommitment)
	if commit.Severity != SeverityCritical {
		t.Fatalf("100%% CPU commitment should be critical, got %q", commit.Severity)
	}
	// Utilization findings must be absent without Prometheus.
	if hasCapType(got, IssueHighCPUUtilization) {
		t.Fatal("no utilization findings should appear without Prometheus")
	}
}

func TestAnalyzeCapacity_HealthyNode(t *testing.T) {
	n := NodeCapacity{
		Name: "worker-4", AllocatableCPUMilli: 4000, AllocatableMemBytes: 8 << 30,
		MaxPods: 110, PodCount: 20, RequestedCPUMilli: 1000, RequestedMemBytes: 2 << 30,
		HasUtilization: true,
		// Oscillating but non-trending: low utilization with ~zero net slope, so
		// no saturation is predicted.
		CPUUtilSeries: series(60, 0.30, 0.31, 0.30),
		MemUtilSeries: series(60, 0.40, 0.41, 0.40),
	}
	got := AnalyzeCapacity(capSnap(true, n))
	if len(got.Findings) != 0 {
		t.Fatalf("healthy node should produce no findings, got %+v", got.Findings)
	}
}

func requireCapFinding(t *testing.T, r CapacityReport, typ CapacityIssueType) CapacityFinding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Type == typ {
			return f
		}
	}
	t.Fatalf("expected a %q finding, got %+v", typ, r.Findings)
	return CapacityFinding{}
}

func hasCapType(r CapacityReport, typ CapacityIssueType) bool {
	for _, f := range r.Findings {
		if f.Type == typ {
			return true
		}
	}
	return false
}
