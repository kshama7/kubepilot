package analysis

import (
	"testing"
	"time"
)

func upSnap(current string, served ...ServedAPI) UpgradeSnapshot {
	return UpgradeSnapshot{
		ClusterID: "c", CurrentVersion: current, APIServerReachable: true,
		CollectedAt: time.Now(), ServedAPIs: served,
	}
}

func served(group, version, kind string, count int) ServedAPI {
	return ServedAPI{Group: group, Version: version, Kind: kind, InstanceCount: count, InstancesKnown: true}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		ok           bool
	}{
		{"v1.24.7", 1, 24, true},
		{"1.25", 1, 25, true},
		{"1.27.3-gke.100", 1, 27, true},
		{"v1.28.2+k3s1", 1, 28, true},
		{"garbage", 0, 0, false},
		{"1", 0, 0, false},
	}
	for _, c := range cases {
		v, ok := parseVersion(c.in)
		if ok != c.ok || (ok && (v.major != c.major || v.minor != c.minor)) {
			t.Errorf("parseVersion(%q)=%+v,%v want %d.%d,%v", c.in, v, ok, c.major, c.minor, c.ok)
		}
	}
}

func TestAnalyzeUpgrade_RemovedAPI(t *testing.T) {
	// policy/v1beta1 PodDisruptionBudget served on a 1.24 cluster, target 1.25.
	snap := upSnap("v1.24.9", served("policy", "v1beta1", "PodDisruptionBudget", 4))
	got := AnalyzeUpgrade(snap, "1.25")

	if got.TargetVersion != "1.25" {
		t.Fatalf("expected target 1.25, got %q", got.TargetVersion)
	}
	f := requireUpFinding(t, got, IssueRemovedAPI)
	if f.Severity != SeverityCritical {
		t.Fatalf("removed API should be critical, got %q", f.Severity)
	}
	if f.Replacement != "policy/v1" || f.Instances != 4 {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if got.Summary.UpgradeSafe {
		t.Fatal("cluster with removed API in use should not be upgrade-safe")
	}
	if got.Summary.AffectedInstances != 4 {
		t.Fatalf("expected 4 affected instances, got %d", got.Summary.AffectedInstances)
	}
}

func TestAnalyzeUpgrade_DeprecatedButNotRemoved(t *testing.T) {
	// PDB is deprecated in 1.21, removed in 1.25. Target 1.23 → deprecated only.
	snap := upSnap("v1.22.0", served("policy", "v1beta1", "PodDisruptionBudget", 2))
	got := AnalyzeUpgrade(snap, "1.23")

	f := requireUpFinding(t, got, IssueDeprecatedAPI)
	if f.Severity != SeverityWarning {
		t.Fatalf("deprecated-not-removed should be warning, got %q", f.Severity)
	}
	if !got.Summary.UpgradeSafe {
		t.Fatal("deprecated-but-not-removed should still be upgrade-safe")
	}
}

func TestAnalyzeUpgrade_DefaultTargetIsNextMinor(t *testing.T) {
	snap := upSnap("v1.21.5", served("batch", "v1beta1", "CronJob", 1))
	got := AnalyzeUpgrade(snap, "") // default target → 1.22
	if got.TargetVersion != "1.22" {
		t.Fatalf("expected default target 1.22, got %q", got.TargetVersion)
	}
	// CronJob batch/v1beta1 removed in 1.25, so at 1.22 it is only deprecated.
	requireUpFinding(t, got, IssueDeprecatedAPI)
}

func TestAnalyzeUpgrade_CleanCluster(t *testing.T) {
	// A served API not in the registry produces nothing.
	snap := upSnap("v1.28.0", served("apps", "v1", "Deployment", 10))
	got := AnalyzeUpgrade(snap, "1.29")
	if len(got.Findings) != 0 {
		t.Fatalf("expected no findings for clean cluster, got %+v", got.Findings)
	}
	if !got.Summary.UpgradeSafe {
		t.Fatal("clean cluster should be upgrade-safe")
	}
}

func TestAnalyzeUpgrade_UnresolvableTarget(t *testing.T) {
	snap := upSnap("garbage", served("policy", "v1beta1", "PodDisruptionBudget", 1))
	got := AnalyzeUpgrade(snap, "") // can't default without a parseable current
	if got.Summary.TargetResolved {
		t.Fatal("target should be unresolved for garbage current version")
	}
	if len(got.Findings) != 0 {
		t.Fatal("no findings should be produced without a resolved target")
	}
}

func TestAnalyzeUpgrade_InstancesUnknown(t *testing.T) {
	snap := upSnap("v1.24.0", ServedAPI{Group: "batch", Version: "v1beta1", Kind: "CronJob", InstancesKnown: false})
	got := AnalyzeUpgrade(snap, "1.25")
	f := requireUpFinding(t, got, IssueRemovedAPI)
	if f.Instances != 0 {
		t.Fatalf("unknown instances should serialize as 0/omitted, got %d", f.Instances)
	}
	if got.Summary.AffectedInstances != 0 {
		t.Fatalf("unknown instances should not inflate the affected count, got %d", got.Summary.AffectedInstances)
	}
}

func TestVersionAtLeast(t *testing.T) {
	v := parsedVersion{1, 25}
	if !versionAtLeast(v, "1.22") {
		t.Error("1.25 should be >= 1.22")
	}
	if versionAtLeast(v, "1.26") {
		t.Error("1.25 should not be >= 1.26")
	}
	if !versionAtLeast(v, "1.25") {
		t.Error("1.25 should be >= 1.25")
	}
}

func requireUpFinding(t *testing.T, r UpgradeReport, typ UpgradeIssueType) UpgradeFinding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Type == typ {
			return f
		}
	}
	t.Fatalf("expected a %q finding, got %+v", typ, r.Findings)
	return UpgradeFinding{}
}
