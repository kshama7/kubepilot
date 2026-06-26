package analysis

import (
	"testing"
	"time"
)

// wellSpecified returns a container with sensible requests and limits and no
// rightsizing pressure.
func wellSpecified(name string) ContainerResources {
	return ContainerResources{
		Name:     name,
		Requests: ResourceQuantities{CPUMilli: 250, MemBytes: 256 << 20, CPUSet: true, MemSet: true},
		Limits:   ResourceQuantities{CPUMilli: 500, MemBytes: 512 << 20, CPUSet: true, MemSet: true},
	}
}

func resSnap(metrics bool, pods ...PodResources) ResourceSnapshot {
	return ResourceSnapshot{
		ClusterID: "c", APIServerReachable: true, MetricsAvailable: metrics,
		CollectedAt: time.Now(), Pods: pods,
	}
}

func TestAnalyzeResources_WellSpecified(t *testing.T) {
	pod := PodResources{Namespace: "default", Name: "web", QOSClass: "Burstable",
		Containers: []ContainerResources{wellSpecified("web")}}
	got := AnalyzeResources(resSnap(false, pod))
	if len(got.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", got.Findings)
	}
	if got.Summary.TotalContainers != 1 {
		t.Fatalf("expected 1 container counted, got %d", got.Summary.TotalContainers)
	}
}

func TestAnalyzeResources_MissingRequestsAndMemLimit(t *testing.T) {
	c := ContainerResources{Name: "app"} // nothing set
	pod := PodResources{Namespace: "default", Name: "bare", QOSClass: "Burstable",
		Containers: []ContainerResources{c}}
	got := AnalyzeResources(resSnap(false, pod))

	requireResFinding(t, got, IssueMissingCPURequest)
	requireResFinding(t, got, IssueMissingMemoryRequest)
	requireResFinding(t, got, IssueMissingMemoryLimit)
}

func TestAnalyzeResources_BestEffortCollapsesToOneFinding(t *testing.T) {
	pod := PodResources{Namespace: "default", Name: "be", QOSClass: "BestEffort",
		Containers: []ContainerResources{{Name: "a"}, {Name: "b"}}}
	got := AnalyzeResources(resSnap(false, pod))

	if len(got.Findings) != 1 {
		t.Fatalf("BestEffort pod should yield exactly one finding, got %d: %+v", len(got.Findings), got.Findings)
	}
	if got.Findings[0].Type != IssueBestEffortQoS {
		t.Fatalf("expected BestEffortQoS, got %q", got.Findings[0].Type)
	}
	if got.Summary.TotalContainers != 2 {
		t.Fatalf("expected both containers counted, got %d", got.Summary.TotalContainers)
	}
}

func TestAnalyzeResources_HighLimitRatio(t *testing.T) {
	c := ContainerResources{
		Name:     "bursty",
		Requests: ResourceQuantities{CPUMilli: 100, MemBytes: 128 << 20, CPUSet: true, MemSet: true},
		Limits:   ResourceQuantities{CPUMilli: 800, MemBytes: 256 << 20, CPUSet: true, MemSet: true},
	}
	pod := PodResources{Namespace: "default", Name: "b", QOSClass: "Burstable",
		Containers: []ContainerResources{c}}
	got := AnalyzeResources(resSnap(false, pod))
	f := requireResFinding(t, got, IssueHighLimitRatio)
	if f.Severity != SeverityInfo {
		t.Fatalf("high limit ratio should be info, got %q", f.Severity)
	}
	if f.Details["ratio"].(float64) != 8.0 {
		t.Fatalf("expected ratio 8.0, got %v", f.Details["ratio"])
	}
}

func TestAnalyzeResources_RightsizingSkippedWithoutMetrics(t *testing.T) {
	c := wellSpecified("web")
	c.HasUsage = true
	c.UsageCPUMilli = 10 // way under 250m request
	pod := PodResources{Namespace: "default", Name: "web", QOSClass: "Burstable",
		Containers: []ContainerResources{c}}

	// metrics flag false → no rightsizing even though usage is present.
	got := AnalyzeResources(resSnap(false, pod))
	if hasResType(got, IssueCPUOverProvisioned) {
		t.Fatal("must not emit rightsizing when MetricsAvailable is false")
	}
}

func TestAnalyzeResources_CPUOverProvisioned(t *testing.T) {
	c := wellSpecified("web") // 250m request
	c.HasUsage = true
	c.UsageCPUMilli = 20 // 8% of request → over-provisioned
	c.UsageMemBytes = 200 << 20
	pod := PodResources{Namespace: "default", Name: "web", QOSClass: "Burstable",
		Containers: []ContainerResources{c}}

	got := AnalyzeResources(resSnap(true, pod))
	f := requireResFinding(t, got, IssueCPUOverProvisioned)
	// suggested = ceil(20 * 1.2) = 24; reclaimable = 250 - 24 = 226.
	if f.Details["suggestedMilli"].(int64) != 24 {
		t.Fatalf("expected suggested 24m, got %v", f.Details["suggestedMilli"])
	}
	if got.Summary.ReclaimableCPUMilli != 226 {
		t.Fatalf("expected 226m reclaimable, got %d", got.Summary.ReclaimableCPUMilli)
	}
}

func TestAnalyzeResources_MemoryOverProvisioned(t *testing.T) {
	c := wellSpecified("web") // 256Mi request
	c.HasUsage = true
	c.UsageCPUMilli = 240      // near request → no CPU finding
	c.UsageMemBytes = 32 << 20 // 32Mi, ~12% of 256Mi request
	pod := PodResources{Namespace: "default", Name: "web", QOSClass: "Burstable",
		Containers: []ContainerResources{c}}

	got := AnalyzeResources(resSnap(true, pod))
	requireResFinding(t, got, IssueMemoryOverProvisioned)
	if got.Summary.ReclaimableMemoryBytes <= 0 {
		t.Fatalf("expected positive reclaimable memory, got %d", got.Summary.ReclaimableMemoryBytes)
	}
	if hasResType(got, IssueCPUOverProvisioned) {
		t.Fatal("CPU near request should not be flagged over-provisioned")
	}
}

func TestAnalyzeResources_TinyRequestsSkipRightsizing(t *testing.T) {
	c := ContainerResources{
		Name:     "tiny",
		Requests: ResourceQuantities{CPUMilli: 10, MemBytes: 16 << 20, CPUSet: true, MemSet: true},
		Limits:   ResourceQuantities{CPUMilli: 20, MemBytes: 32 << 20, CPUSet: true, MemSet: true},
		HasUsage: true, UsageCPUMilli: 1, UsageMemBytes: 1 << 20,
	}
	pod := PodResources{Namespace: "default", Name: "tiny", QOSClass: "Burstable",
		Containers: []ContainerResources{c}}
	got := AnalyzeResources(resSnap(true, pod))
	if hasResType(got, IssueCPUOverProvisioned) || hasResType(got, IssueMemoryOverProvisioned) {
		t.Fatal("containers below the rightsizing floor should not be flagged")
	}
}

// --- helpers ---

func requireResFinding(t *testing.T, r ResourceReport, typ ResourceIssueType) ResourceFinding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Type == typ {
			return f
		}
	}
	t.Fatalf("expected a %q finding, got %+v", typ, r.Findings)
	return ResourceFinding{}
}

func hasResType(r ResourceReport, typ ResourceIssueType) bool {
	for _, f := range r.Findings {
		if f.Type == typ {
			return true
		}
	}
	return false
}
