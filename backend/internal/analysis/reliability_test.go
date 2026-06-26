package analysis

import (
	"testing"
	"time"
)

func probedContainer(name string) ContainerProbes {
	return ContainerProbes{Name: name, HasReadiness: true, HasLiveness: true}
}

func relSnap(workloads ...WorkloadSpec) ReliabilitySnapshot {
	return ReliabilitySnapshot{
		ClusterID: "c", APIServerReachable: true, CollectedAt: time.Now(), Workloads: workloads,
	}
}

// hardened is a well-configured multi-replica workload with no reliability gaps.
func hardened(name string) WorkloadSpec {
	return WorkloadSpec{
		Namespace: "prod", Name: name, Kind: "Deployment", Replicas: 3,
		Containers:        []ContainerProbes{probedContainer("app")},
		HasTopologySpread: true,
		PDBs:              []PDBRef{{Name: name + "-pdb", MinAvailable: "2", AllowsDisruption: true}},
	}
}

func TestAnalyzeReliability_Hardened(t *testing.T) {
	got := AnalyzeReliability(relSnap(hardened("web")))
	if len(got.Findings) != 0 {
		t.Fatalf("expected no findings for hardened workload, got %+v", got.Findings)
	}
	if got.Summary.MultiReplicaWorkloads != 1 || got.Summary.MultiReplicaWithoutPDB != 0 {
		t.Fatalf("unexpected summary: %+v", got.Summary)
	}
}

func TestAnalyzeReliability_SingleReplica(t *testing.T) {
	w := WorkloadSpec{
		Namespace: "prod", Name: "api", Kind: "Deployment", Replicas: 1,
		Containers: []ContainerProbes{probedContainer("api")},
	}
	got := AnalyzeReliability(relSnap(w))
	f := requireRelFinding(t, got, IssueSingleReplica)
	if f.Severity != SeverityWarning {
		t.Fatalf("single replica should be warning, got %q", f.Severity)
	}
	// Single-replica path must not also demand a PDB or spread constraints.
	if hasRelType(got, IssueMissingPDB) || hasRelType(got, IssueNoSpreadConstraints) {
		t.Fatal("single-replica workload should not trigger PDB/spread findings")
	}
}

func TestAnalyzeReliability_MissingPDBAndSpread(t *testing.T) {
	w := WorkloadSpec{
		Namespace: "prod", Name: "api", Kind: "Deployment", Replicas: 4,
		Containers: []ContainerProbes{probedContainer("api")},
		// no PDB, no anti-affinity, no topology spread
	}
	got := AnalyzeReliability(relSnap(w))
	requireRelFinding(t, got, IssueMissingPDB)
	requireRelFinding(t, got, IssueNoSpreadConstraints)
	if got.Summary.MultiReplicaWithoutPDB != 1 {
		t.Fatalf("expected 1 multi-replica workload without PDB, got %d", got.Summary.MultiReplicaWithoutPDB)
	}
}

func TestAnalyzeReliability_AntiAffinitySatisfiesSpread(t *testing.T) {
	w := WorkloadSpec{
		Namespace: "prod", Name: "api", Kind: "Deployment", Replicas: 2,
		Containers:         []ContainerProbes{probedContainer("api")},
		HasPodAntiAffinity: true,
		PDBs:               []PDBRef{{Name: "api-pdb", MaxUnavailable: "1", AllowsDisruption: true}},
	}
	got := AnalyzeReliability(relSnap(w))
	if hasRelType(got, IssueNoSpreadConstraints) {
		t.Fatal("pod anti-affinity should satisfy the spread requirement")
	}
}

func TestAnalyzeReliability_PDBBlocksDrain(t *testing.T) {
	w := WorkloadSpec{
		Namespace: "prod", Name: "api", Kind: "StatefulSet", Replicas: 3,
		Containers:        []ContainerProbes{probedContainer("api")},
		HasTopologySpread: true,
		PDBs:              []PDBRef{{Name: "api-pdb", MinAvailable: "3", AllowsDisruption: false}},
	}
	got := AnalyzeReliability(relSnap(w))
	f := requireRelFinding(t, got, IssuePDBBlocksDrain)
	if f.Details["pdb"] != "api-pdb" {
		t.Fatalf("expected pdb detail, got %+v", f.Details)
	}
	// A blocking PDB still counts as PDB coverage — must not also report MissingPDB.
	if hasRelType(got, IssueMissingPDB) {
		t.Fatal("workload with a PDB should not report MissingPDB")
	}
}

func TestAnalyzeReliability_MissingProbes(t *testing.T) {
	w := WorkloadSpec{
		Namespace: "default", Name: "worker", Kind: "Deployment", Replicas: 1,
		Containers: []ContainerProbes{{Name: "worker"}}, // no probes
	}
	got := AnalyzeReliability(relSnap(w))
	readiness := requireRelFinding(t, got, IssueMissingReadinessProbe)
	liveness := requireRelFinding(t, got, IssueMissingLivenessProbe)
	if readiness.Severity != SeverityWarning {
		t.Fatalf("missing readiness should be warning, got %q", readiness.Severity)
	}
	if liveness.Severity != SeverityInfo {
		t.Fatalf("missing liveness should be info, got %q", liveness.Severity)
	}
}

func TestAnalyzeReliability_InitContainersSkipProbes(t *testing.T) {
	w := WorkloadSpec{
		Namespace: "default", Name: "app", Kind: "Deployment", Replicas: 3,
		HasTopologySpread: true,
		PDBs:              []PDBRef{{Name: "app-pdb", MaxUnavailable: "1", AllowsDisruption: true}},
		Containers: []ContainerProbes{
			{Name: "migrate", Init: true}, // init: probes not expected
			probedContainer("app"),
		},
	}
	got := AnalyzeReliability(relSnap(w))
	if hasRelType(got, IssueMissingReadinessProbe) || hasRelType(got, IssueMissingLivenessProbe) {
		t.Fatalf("init containers should not trigger probe findings: %+v", got.Findings)
	}
}

func TestAnalyzeReliability_SortingCriticalFirst(t *testing.T) {
	w := WorkloadSpec{
		Namespace: "default", Name: "mixed", Kind: "Deployment", Replicas: 2,
		Containers: []ContainerProbes{{Name: "c"}}, // missing readiness (warn) + liveness (info)
	}
	got := AnalyzeReliability(relSnap(w))
	if len(got.Findings) < 2 {
		t.Fatalf("expected multiple findings, got %d", len(got.Findings))
	}
	// info findings must never sort ahead of warnings.
	for i := 1; i < len(got.Findings); i++ {
		if severityRank(got.Findings[i-1].Severity) > severityRank(got.Findings[i].Severity) {
			t.Fatalf("findings not sorted by severity: %+v", got.Findings)
		}
	}
}

// --- helpers ---

func requireRelFinding(t *testing.T, r ReliabilityReport, typ ReliabilityIssueType) ReliabilityFinding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Type == typ {
			return f
		}
	}
	t.Fatalf("expected a %q finding, got %+v", typ, r.Findings)
	return ReliabilityFinding{}
}

func hasRelType(r ReliabilityReport, typ ReliabilityIssueType) bool {
	for _, f := range r.Findings {
		if f.Type == typ {
			return true
		}
	}
	return false
}
