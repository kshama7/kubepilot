package analysis

import (
	"sort"
	"strings"
	"time"
)

// SecurityIssueType identifies a category of pod/container security weakness.
type SecurityIssueType string

const (
	IssuePrivileged               SecurityIssueType = "PrivilegedContainer"
	IssueDangerousCapability      SecurityIssueType = "DangerousCapability"
	IssueRunAsRoot                SecurityIssueType = "RunAsRoot"
	IssueAllowPrivilegeEscalation SecurityIssueType = "AllowPrivilegeEscalation"
	IssuePlaintextSecretEnv       SecurityIssueType = "PlaintextSecretEnv"
	IssueHostNamespace            SecurityIssueType = "HostNamespace"
	IssueHostPathVolume           SecurityIssueType = "HostPathVolume"
	IssueWritableRootFS           SecurityIssueType = "WritableRootFilesystem"
	IssueNoCapabilityDrop         SecurityIssueType = "NoCapabilityDrop"
)

// criticalCapabilities grant near-root power; adding them is a critical finding.
var criticalCapabilities = map[string]bool{
	"ALL":       true,
	"SYS_ADMIN": true,
}

// dangerousCapabilities are powerful but narrower than the critical set.
var dangerousCapabilities = map[string]bool{
	"NET_ADMIN":       true,
	"NET_RAW":         true,
	"SYS_PTRACE":      true,
	"SYS_MODULE":      true,
	"SYS_BOOT":        true,
	"SYS_TIME":        true,
	"DAC_READ_SEARCH": true,
	"DAC_OVERRIDE":    true,
	"SETUID":          true,
	"SETGID":          true,
	"LINUX_IMMUTABLE": true,
	"BPF":             true,
}

// secretEnvHints are substrings in an env var name that suggest a credential.
var secretEnvHints = []string{
	"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "API_KEY",
	"ACCESS_KEY", "ACCESSKEY", "PRIVATE_KEY", "CREDENTIAL", "PASSPHRASE",
}

// sensitiveHostPaths mounted into a container are a critical escape vector.
var sensitiveHostPaths = []string{
	"docker.sock", "containerd.sock", "crio.sock",
	"/var/lib/kubelet", "/var/run", "/proc", "/root", "/etc/kubernetes",
}

// EnvVarRef is the security-relevant view of one env var.
type EnvVarRef struct {
	Name            string `json:"name"`
	HasLiteralValue bool   `json:"hasLiteralValue"` // value set inline (not valueFrom)
}

// HostPathVolume is a hostPath volume declared on a pod.
type HostPathVolume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ContainerSecurity is the security-relevant view of one container.
type ContainerSecurity struct {
	Name                     string      `json:"name"`
	Init                     bool        `json:"init,omitempty"`
	Privileged               bool        `json:"privileged"`
	AllowPrivilegeEscalation *bool       `json:"allowPrivilegeEscalation,omitempty"`
	RunAsNonRoot             *bool       `json:"runAsNonRoot,omitempty"`
	RunAsUser                *int64      `json:"runAsUser,omitempty"`
	ReadOnlyRootFilesystem   *bool       `json:"readOnlyRootFilesystem,omitempty"`
	AddedCapabilities        []string    `json:"addedCapabilities,omitempty"`
	DropsAll                 bool        `json:"dropsAll"`
	Env                      []EnvVarRef `json:"env,omitempty"`
}

// PodSecurity is the security-relevant view of one pod.
type PodSecurity struct {
	Namespace       string              `json:"namespace"`
	Name            string              `json:"name"`
	HostNetwork     bool                `json:"hostNetwork"`
	HostPID         bool                `json:"hostPid"`
	HostIPC         bool                `json:"hostIpc"`
	RunAsNonRoot    *bool               `json:"runAsNonRoot,omitempty"` // pod-level default
	RunAsUser       *int64              `json:"runAsUser,omitempty"`
	HostPathVolumes []HostPathVolume    `json:"hostPathVolumes,omitempty"`
	Containers      []ContainerSecurity `json:"containers"`
}

// SecuritySnapshot is the raw observed state the security rules evaluate.
type SecuritySnapshot struct {
	ClusterID          string        `json:"clusterId"`
	Namespace          string        `json:"namespace"`
	APIServerReachable bool          `json:"apiServerReachable"`
	APIServerError     string        `json:"apiServerError,omitempty"`
	Pods               []PodSecurity `json:"pods"`
	CollectedAt        time.Time     `json:"collectedAt"`
}

// SecurityFinding is a single deterministic security weakness.
type SecurityFinding struct {
	Type      SecurityIssueType `json:"type"`
	Severity  Severity          `json:"severity"`
	Namespace string            `json:"namespace"`
	Pod       string            `json:"pod"`
	Container string            `json:"container,omitempty"`
	Message   string            `json:"message"`
	Details   map[string]any    `json:"details,omitempty"`
}

// SecuritySummary is a quick-glance posture rollup.
type SecuritySummary struct {
	TotalPods          int                       `json:"totalPods"`
	PodsWithIssues     int                       `json:"podsWithIssues"`
	PrivilegedPods     int                       `json:"privilegedPods"`
	FindingsBySeverity map[Severity]int          `json:"findingsBySeverity"`
	FindingsByType     map[SecurityIssueType]int `json:"findingsByType"`
}

// SecurityReport is the full result of a security-analysis run.
type SecurityReport struct {
	ClusterID   string            `json:"clusterId"`
	Namespace   string            `json:"namespace"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Summary     SecuritySummary   `json:"summary"`
	Findings    []SecurityFinding `json:"findings"`
}

// AnalyzeSecurity evaluates the deterministic security rule set over a snapshot.
// It performs no I/O.
func AnalyzeSecurity(snap SecuritySnapshot) SecurityReport {
	findings := make([]SecurityFinding, 0)
	podsWithIssues := 0
	privilegedPods := 0

	for _, pod := range snap.Pods {
		before := len(findings)
		findings = append(findings, evaluatePodSecurity(pod)...)
		if len(findings) > before {
			podsWithIssues++
		}
		if podHasPrivileged(pod) {
			privilegedPods++
		}
	}

	sortSecurityFindings(findings)

	return SecurityReport{
		ClusterID:   snap.ClusterID,
		Namespace:   snap.Namespace,
		GeneratedAt: time.Now().UTC(),
		Findings:    findings,
		Summary: SecuritySummary{
			TotalPods:          len(snap.Pods),
			PodsWithIssues:     podsWithIssues,
			PrivilegedPods:     privilegedPods,
			FindingsBySeverity: securityCountBySeverity(findings),
			FindingsByType:     securityCountByType(findings),
		},
	}
}

func evaluatePodSecurity(pod PodSecurity) []SecurityFinding {
	var out []SecurityFinding

	// Pod-level: host namespaces break container isolation.
	if pod.HostNetwork {
		out = append(out, podSecFinding(pod, IssueHostNamespace, SeverityWarning,
			"pod uses the host network namespace", map[string]any{"namespace": "hostNetwork"}))
	}
	if pod.HostPID {
		out = append(out, podSecFinding(pod, IssueHostNamespace, SeverityWarning,
			"pod uses the host PID namespace", map[string]any{"namespace": "hostPID"}))
	}
	if pod.HostIPC {
		out = append(out, podSecFinding(pod, IssueHostNamespace, SeverityWarning,
			"pod uses the host IPC namespace", map[string]any{"namespace": "hostIPC"}))
	}
	for _, hp := range pod.HostPathVolumes {
		sev := SeverityWarning
		if isSensitiveHostPath(hp.Path) {
			sev = SeverityCritical
		}
		out = append(out, podSecFinding(pod, IssueHostPathVolume, sev,
			"pod mounts a hostPath volume from the node filesystem",
			map[string]any{"volume": hp.Name, "path": hp.Path}))
	}

	for _, c := range pod.Containers {
		out = append(out, evaluateContainerSecurity(pod, c)...)
	}
	return out
}

func evaluateContainerSecurity(pod PodSecurity, c ContainerSecurity) []SecurityFinding {
	var out []SecurityFinding

	if c.Privileged {
		out = append(out, containerSecFinding(pod, c, IssuePrivileged, SeverityCritical,
			"container runs in privileged mode (full host access)", nil))
	}

	for _, cap := range c.AddedCapabilities {
		norm := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(cap), "CAP_"))
		switch {
		case criticalCapabilities[norm]:
			out = append(out, containerSecFinding(pod, c, IssueDangerousCapability, SeverityCritical,
				"container adds the "+norm+" capability (near-root)", map[string]any{"capability": norm}))
		case dangerousCapabilities[norm]:
			out = append(out, containerSecFinding(pod, c, IssueDangerousCapability, SeverityWarning,
				"container adds the powerful "+norm+" capability", map[string]any{"capability": norm}))
		}
	}

	// runAsRoot — but a privileged container is already the headline; don't pile on.
	if !c.Privileged && containerRunsAsRoot(pod, c) {
		out = append(out, containerSecFinding(pod, c, IssueRunAsRoot, SeverityWarning,
			"container may run as root (runAsNonRoot not set and no non-zero runAsUser)", nil))
	}

	// allowPrivilegeEscalation: explicit true is a warning; unset defaults true so
	// it's worth an info nudge. Privileged containers can always escalate anyway.
	if !c.Privileged {
		switch {
		case c.AllowPrivilegeEscalation != nil && *c.AllowPrivilegeEscalation:
			out = append(out, containerSecFinding(pod, c, IssueAllowPrivilegeEscalation, SeverityWarning,
				"allowPrivilegeEscalation is explicitly true", nil))
		case c.AllowPrivilegeEscalation == nil:
			out = append(out, containerSecFinding(pod, c, IssueAllowPrivilegeEscalation, SeverityInfo,
				"allowPrivilegeEscalation is unset (defaults to true); set it to false", nil))
		}
	}

	for _, e := range c.Env {
		if e.HasLiteralValue && looksLikeSecret(e.Name) {
			out = append(out, containerSecFinding(pod, c, IssuePlaintextSecretEnv, SeverityWarning,
				"env var "+e.Name+" appears to hold a secret inline; use a Secret with valueFrom",
				map[string]any{"envVar": e.Name}))
		}
	}

	if c.ReadOnlyRootFilesystem == nil || !*c.ReadOnlyRootFilesystem {
		out = append(out, containerSecFinding(pod, c, IssueWritableRootFS, SeverityInfo,
			"root filesystem is writable; set readOnlyRootFilesystem: true", nil))
	}
	if !c.DropsAll {
		out = append(out, containerSecFinding(pod, c, IssueNoCapabilityDrop, SeverityInfo,
			"container does not drop ALL capabilities", nil))
	}
	return out
}

// containerRunsAsRoot resolves runAsNonRoot/runAsUser with pod-level defaults.
func containerRunsAsRoot(pod PodSecurity, c ContainerSecurity) bool {
	nonRoot := c.RunAsNonRoot
	if nonRoot == nil {
		nonRoot = pod.RunAsNonRoot
	}
	if nonRoot != nil && *nonRoot {
		return false
	}
	uid := c.RunAsUser
	if uid == nil {
		uid = pod.RunAsUser
	}
	// Explicit non-zero UID means not root even if runAsNonRoot is unset.
	if uid != nil && *uid != 0 {
		return false
	}
	return true
}

func podHasPrivileged(pod PodSecurity) bool {
	for _, c := range pod.Containers {
		if c.Privileged {
			return true
		}
	}
	return false
}

func looksLikeSecret(name string) bool {
	up := strings.ToUpper(name)
	for _, hint := range secretEnvHints {
		if strings.Contains(up, hint) {
			return true
		}
	}
	return false
}

func isSensitiveHostPath(path string) bool {
	if path == "/" {
		return true
	}
	for _, s := range sensitiveHostPaths {
		if strings.Contains(path, s) {
			return true
		}
	}
	return false
}

func podSecFinding(pod PodSecurity, t SecurityIssueType, sev Severity, msg string, details map[string]any) SecurityFinding {
	return SecurityFinding{
		Type: t, Severity: sev,
		Namespace: pod.Namespace, Pod: pod.Name,
		Message: msg, Details: details,
	}
}

func containerSecFinding(pod PodSecurity, c ContainerSecurity, t SecurityIssueType, sev Severity, msg string, details map[string]any) SecurityFinding {
	return SecurityFinding{
		Type: t, Severity: sev,
		Namespace: pod.Namespace, Pod: pod.Name, Container: c.Name,
		Message: msg, Details: details,
	}
}

func sortSecurityFindings(f []SecurityFinding) {
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

func securityCountBySeverity(f []SecurityFinding) map[Severity]int {
	m := map[Severity]int{}
	for _, x := range f {
		m[x.Severity]++
	}
	return m
}

func securityCountByType(f []SecurityFinding) map[SecurityIssueType]int {
	m := map[SecurityIssueType]int{}
	for _, x := range f {
		m[x.Type]++
	}
	return m
}
