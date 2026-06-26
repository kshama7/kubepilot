package analysis

import (
	"testing"
	"time"
)

func boolp(b bool) *bool    { return &b }
func int64p(i int64) *int64 { return &i }

func secSnap(pods ...PodSecurity) SecuritySnapshot {
	return SecuritySnapshot{ClusterID: "c", APIServerReachable: true, CollectedAt: time.Now(), Pods: pods}
}

// hardenedContainer trips none of the warning/critical rules (only info-level
// ones if any).
func hardenedContainer(name string) ContainerSecurity {
	return ContainerSecurity{
		Name:                     name,
		AllowPrivilegeEscalation: boolp(false),
		RunAsNonRoot:             boolp(true),
		ReadOnlyRootFilesystem:   boolp(true),
		DropsAll:                 true,
	}
}

func TestAnalyzeSecurity_Hardened(t *testing.T) {
	pod := PodSecurity{Namespace: "prod", Name: "web", Containers: []ContainerSecurity{hardenedContainer("web")}}
	got := AnalyzeSecurity(secSnap(pod))
	if len(got.Findings) != 0 {
		t.Fatalf("hardened pod should have no findings, got %+v", got.Findings)
	}
}

func TestAnalyzeSecurity_Privileged(t *testing.T) {
	c := hardenedContainer("web")
	c.Privileged = true
	pod := PodSecurity{Namespace: "prod", Name: "web", Containers: []ContainerSecurity{c}}
	got := AnalyzeSecurity(secSnap(pod))

	f := requireSecFinding(t, got, IssuePrivileged)
	if f.Severity != SeverityCritical {
		t.Fatalf("privileged should be critical, got %q", f.Severity)
	}
	if got.Summary.PrivilegedPods != 1 {
		t.Fatalf("expected 1 privileged pod, got %d", got.Summary.PrivilegedPods)
	}
	// Privileged container should not also be nagged for runAsRoot / escalation.
	if hasSecType(got, IssueRunAsRoot) || hasSecType(got, IssueAllowPrivilegeEscalation) {
		t.Fatal("privileged container should suppress redundant root/escalation findings")
	}
}

func TestAnalyzeSecurity_CapabilitiesSeverity(t *testing.T) {
	c := hardenedContainer("web")
	c.AddedCapabilities = []string{"SYS_ADMIN", "NET_ADMIN", "CHOWN"}
	pod := PodSecurity{Namespace: "prod", Name: "web", Containers: []ContainerSecurity{c}}
	got := AnalyzeSecurity(secSnap(pod))

	var sysAdmin, netAdmin Severity
	chownSeen := false
	for _, f := range got.Findings {
		if f.Type == IssueDangerousCapability {
			switch f.Details["capability"] {
			case "SYS_ADMIN":
				sysAdmin = f.Severity
			case "NET_ADMIN":
				netAdmin = f.Severity
			case "CHOWN":
				chownSeen = true
			}
		}
	}
	if sysAdmin != SeverityCritical {
		t.Fatalf("SYS_ADMIN should be critical, got %q", sysAdmin)
	}
	if netAdmin != SeverityWarning {
		t.Fatalf("NET_ADMIN should be warning, got %q", netAdmin)
	}
	if chownSeen {
		t.Fatal("benign capability CHOWN should not be flagged")
	}
}

func TestAnalyzeSecurity_CapPrefixNormalized(t *testing.T) {
	c := hardenedContainer("web")
	c.AddedCapabilities = []string{"CAP_SYS_ADMIN"} // some manifests use the CAP_ prefix
	pod := PodSecurity{Namespace: "prod", Name: "web", Containers: []ContainerSecurity{c}}
	got := AnalyzeSecurity(secSnap(pod))
	f := requireSecFinding(t, got, IssueDangerousCapability)
	if f.Severity != SeverityCritical || f.Details["capability"] != "SYS_ADMIN" {
		t.Fatalf("CAP_ prefix should normalize to SYS_ADMIN critical, got %+v", f)
	}
}

func TestAnalyzeSecurity_RunAsRoot(t *testing.T) {
	c := hardenedContainer("web")
	c.RunAsNonRoot = nil // no guarantee against root
	pod := PodSecurity{Namespace: "prod", Name: "web", Containers: []ContainerSecurity{c}}
	got := AnalyzeSecurity(secSnap(pod))
	requireSecFinding(t, got, IssueRunAsRoot)
}

func TestAnalyzeSecurity_NonZeroUIDIsNotRoot(t *testing.T) {
	c := hardenedContainer("web")
	c.RunAsNonRoot = nil
	c.RunAsUser = int64p(1000) // explicit non-root UID
	pod := PodSecurity{Namespace: "prod", Name: "web", Containers: []ContainerSecurity{c}}
	got := AnalyzeSecurity(secSnap(pod))
	if hasSecType(got, IssueRunAsRoot) {
		t.Fatal("explicit non-zero runAsUser should not be flagged as root")
	}
}

func TestAnalyzeSecurity_PodLevelRunAsNonRootInherited(t *testing.T) {
	c := hardenedContainer("web")
	c.RunAsNonRoot = nil // inherit from pod
	pod := PodSecurity{
		Namespace: "prod", Name: "web", RunAsNonRoot: boolp(true),
		Containers: []ContainerSecurity{c},
	}
	got := AnalyzeSecurity(secSnap(pod))
	if hasSecType(got, IssueRunAsRoot) {
		t.Fatal("pod-level runAsNonRoot should satisfy the container")
	}
}

func TestAnalyzeSecurity_PlaintextSecretEnv(t *testing.T) {
	c := hardenedContainer("web")
	c.Env = []EnvVarRef{
		{Name: "DB_PASSWORD", HasLiteralValue: true}, // flagged
		{Name: "API_TOKEN", HasLiteralValue: false},  // from secretRef → ok
		{Name: "LOG_LEVEL", HasLiteralValue: true},   // not secret-like → ok
	}
	pod := PodSecurity{Namespace: "prod", Name: "web", Containers: []ContainerSecurity{c}}
	got := AnalyzeSecurity(secSnap(pod))

	count := 0
	for _, f := range got.Findings {
		if f.Type == IssuePlaintextSecretEnv {
			count++
			if f.Details["envVar"] != "DB_PASSWORD" {
				t.Fatalf("wrong env var flagged: %+v", f.Details)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one plaintext-secret finding, got %d", count)
	}
}

func TestAnalyzeSecurity_HostNamespacesAndPath(t *testing.T) {
	pod := PodSecurity{
		Namespace: "prod", Name: "node-agent",
		HostNetwork: true, HostPID: true,
		HostPathVolumes: []HostPathVolume{
			{Name: "docker", Path: "/var/run/docker.sock"}, // critical
			{Name: "logs", Path: "/var/log/app"},           // warning
		},
		Containers: []ContainerSecurity{hardenedContainer("agent")},
	}
	got := AnalyzeSecurity(secSnap(pod))

	hostNs := 0
	var dockerSev, logsSev Severity
	for _, f := range got.Findings {
		if f.Type == IssueHostNamespace {
			hostNs++
		}
		if f.Type == IssueHostPathVolume {
			switch f.Details["path"] {
			case "/var/run/docker.sock":
				dockerSev = f.Severity
			case "/var/log/app":
				logsSev = f.Severity
			}
		}
	}
	if hostNs != 2 {
		t.Fatalf("expected hostNetwork and hostPID findings, got %d", hostNs)
	}
	if dockerSev != SeverityCritical {
		t.Fatalf("docker.sock hostPath should be critical, got %q", dockerSev)
	}
	if logsSev != SeverityWarning {
		t.Fatalf("ordinary hostPath should be warning, got %q", logsSev)
	}
}

func TestAnalyzeSecurity_InfoLevelHardeningNudges(t *testing.T) {
	// A container that runs as non-root but leaves rootfs writable and keeps caps.
	c := ContainerSecurity{
		Name:                     "web",
		RunAsNonRoot:             boolp(true),
		AllowPrivilegeEscalation: boolp(false),
		// ReadOnlyRootFilesystem unset, DropsAll false
	}
	pod := PodSecurity{Namespace: "prod", Name: "web", Containers: []ContainerSecurity{c}}
	got := AnalyzeSecurity(secSnap(pod))
	requireSecFinding(t, got, IssueWritableRootFS)
	requireSecFinding(t, got, IssueNoCapabilityDrop)
	if got.Summary.FindingsBySeverity[SeverityCritical] != 0 {
		t.Fatal("hardening nudges should not be critical")
	}
}

func requireSecFinding(t *testing.T, r SecurityReport, typ SecurityIssueType) SecurityFinding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Type == typ {
			return f
		}
	}
	t.Fatalf("expected a %q finding, got %+v", typ, r.Findings)
	return SecurityFinding{}
}

func hasSecType(r SecurityReport, typ SecurityIssueType) bool {
	for _, f := range r.Findings {
		if f.Type == typ {
			return true
		}
	}
	return false
}
