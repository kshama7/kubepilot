package analysis

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// UpgradeIssueType identifies the upgrade-readiness verdict for an API.
type UpgradeIssueType string

const (
	IssueRemovedAPI    UpgradeIssueType = "RemovedAPI"
	IssueDeprecatedAPI UpgradeIssueType = "DeprecatedAPI"
)

// APIDeprecation is one curated entry in the deprecated/removed-API registry.
// Versions are Kubernetes minor strings like "1.22". RemovedIn is empty when an
// API is deprecated but not yet scheduled for removal.
type APIDeprecation struct {
	Group        string `json:"group"`
	Version      string `json:"version"`
	Kind         string `json:"kind"`
	DeprecatedIn string `json:"deprecatedIn,omitempty"`
	RemovedIn    string `json:"removedIn,omitempty"`
	Replacement  string `json:"replacement,omitempty"` // e.g. "networking.k8s.io/v1"
}

// apiVersion renders the group/version string ("" group → core/v1 style).
func (d APIDeprecation) apiVersion() string {
	if d.Group == "" {
		return d.Version
	}
	return d.Group + "/" + d.Version
}

// deprecationRegistry is the curated, deterministic source of truth. It covers
// the well-known removals that break real cluster upgrades. It is intentionally
// data, not inference — every entry maps to an upstream API removal.
var deprecationRegistry = []APIDeprecation{
	// Removed in 1.16
	{"extensions", "v1beta1", "Deployment", "1.9", "1.16", "apps/v1"},
	{"extensions", "v1beta1", "DaemonSet", "1.9", "1.16", "apps/v1"},
	{"extensions", "v1beta1", "ReplicaSet", "1.9", "1.16", "apps/v1"},
	{"extensions", "v1beta1", "NetworkPolicy", "1.9", "1.16", "networking.k8s.io/v1"},
	{"extensions", "v1beta1", "PodSecurityPolicy", "1.10", "1.16", "policy/v1beta1"},
	{"apps", "v1beta1", "Deployment", "1.9", "1.16", "apps/v1"},
	{"apps", "v1beta2", "Deployment", "1.9", "1.16", "apps/v1"},

	// Removed in 1.22
	{"extensions", "v1beta1", "Ingress", "1.14", "1.22", "networking.k8s.io/v1"},
	{"networking.k8s.io", "v1beta1", "Ingress", "1.19", "1.22", "networking.k8s.io/v1"},
	{"networking.k8s.io", "v1beta1", "IngressClass", "1.19", "1.22", "networking.k8s.io/v1"},
	{"rbac.authorization.k8s.io", "v1beta1", "ClusterRole", "1.17", "1.22", "rbac.authorization.k8s.io/v1"},
	{"rbac.authorization.k8s.io", "v1beta1", "ClusterRoleBinding", "1.17", "1.22", "rbac.authorization.k8s.io/v1"},
	{"rbac.authorization.k8s.io", "v1beta1", "Role", "1.17", "1.22", "rbac.authorization.k8s.io/v1"},
	{"rbac.authorization.k8s.io", "v1beta1", "RoleBinding", "1.17", "1.22", "rbac.authorization.k8s.io/v1"},
	{"apiextensions.k8s.io", "v1beta1", "CustomResourceDefinition", "1.16", "1.22", "apiextensions.k8s.io/v1"},
	{"apiregistration.k8s.io", "v1beta1", "APIService", "1.19", "1.22", "apiregistration.k8s.io/v1"},
	{"admissionregistration.k8s.io", "v1beta1", "MutatingWebhookConfiguration", "1.16", "1.22", "admissionregistration.k8s.io/v1"},
	{"admissionregistration.k8s.io", "v1beta1", "ValidatingWebhookConfiguration", "1.16", "1.22", "admissionregistration.k8s.io/v1"},
	{"coordination.k8s.io", "v1beta1", "Lease", "1.19", "1.22", "coordination.k8s.io/v1"},
	{"certificates.k8s.io", "v1beta1", "CertificateSigningRequest", "1.19", "1.22", "certificates.k8s.io/v1"},
	{"scheduling.k8s.io", "v1beta1", "PriorityClass", "1.14", "1.22", "scheduling.k8s.io/v1"},
	{"storage.k8s.io", "v1beta1", "CSIDriver", "1.18", "1.22", "storage.k8s.io/v1"},
	{"storage.k8s.io", "v1beta1", "CSINode", "1.17", "1.22", "storage.k8s.io/v1"},
	{"storage.k8s.io", "v1beta1", "StorageClass", "1.19", "1.22", "storage.k8s.io/v1"},
	{"storage.k8s.io", "v1beta1", "VolumeAttachment", "1.19", "1.22", "storage.k8s.io/v1"},

	// Removed in 1.25
	{"policy", "v1beta1", "PodDisruptionBudget", "1.21", "1.25", "policy/v1"},
	{"policy", "v1beta1", "PodSecurityPolicy", "1.21", "1.25", ""},
	{"batch", "v1beta1", "CronJob", "1.21", "1.25", "batch/v1"},
	{"discovery.k8s.io", "v1beta1", "EndpointSlice", "1.21", "1.25", "discovery.k8s.io/v1"},
	{"autoscaling", "v2beta1", "HorizontalPodAutoscaler", "1.23", "1.25", "autoscaling/v2"},
	{"node.k8s.io", "v1beta1", "RuntimeClass", "1.22", "1.25", "node.k8s.io/v1"},

	// Removed in 1.26
	{"autoscaling", "v2beta2", "HorizontalPodAutoscaler", "1.23", "1.26", "autoscaling/v2"},

	// Removed in 1.29 / 1.32 (API Priority and Fairness)
	{"flowcontrol.apiserver.k8s.io", "v1beta1", "FlowSchema", "1.23", "1.29", "flowcontrol.apiserver.k8s.io/v1"},
	{"flowcontrol.apiserver.k8s.io", "v1beta1", "PriorityLevelConfiguration", "1.23", "1.29", "flowcontrol.apiserver.k8s.io/v1"},
	{"flowcontrol.apiserver.k8s.io", "v1beta2", "FlowSchema", "1.26", "1.29", "flowcontrol.apiserver.k8s.io/v1"},
	{"flowcontrol.apiserver.k8s.io", "v1beta2", "PriorityLevelConfiguration", "1.26", "1.29", "flowcontrol.apiserver.k8s.io/v1"},
	{"flowcontrol.apiserver.k8s.io", "v1beta3", "FlowSchema", "1.29", "1.32", "flowcontrol.apiserver.k8s.io/v1"},
	{"flowcontrol.apiserver.k8s.io", "v1beta3", "PriorityLevelConfiguration", "1.29", "1.32", "flowcontrol.apiserver.k8s.io/v1"},
}

type gvk struct{ group, version, kind string }

var registryIndex = func() map[gvk]APIDeprecation {
	m := make(map[gvk]APIDeprecation, len(deprecationRegistry))
	for _, d := range deprecationRegistry {
		m[gvk{d.Group, d.Version, d.Kind}] = d
	}
	return m
}()

// DeprecationRegistry returns a copy of the curated registry so the collector
// knows which served GVKs are worth inspecting.
func DeprecationRegistry() []APIDeprecation {
	out := make([]APIDeprecation, len(deprecationRegistry))
	copy(out, deprecationRegistry)
	return out
}

// ServedAPI is a registry-listed API that the cluster currently serves, with an
// optional live instance count.
type ServedAPI struct {
	Group          string `json:"group"`
	Version        string `json:"version"`
	Kind           string `json:"kind"`
	InstanceCount  int    `json:"instanceCount"`
	InstancesKnown bool   `json:"instancesKnown"`
}

// UpgradeSnapshot is the raw observed state the upgrade rules evaluate.
type UpgradeSnapshot struct {
	ClusterID          string      `json:"clusterId"`
	CurrentVersion     string      `json:"currentVersion"`
	APIServerReachable bool        `json:"apiServerReachable"`
	APIServerError     string      `json:"apiServerError,omitempty"`
	ServedAPIs         []ServedAPI `json:"servedApis"`
	CollectedAt        time.Time   `json:"collectedAt"`
}

// UpgradeFinding flags one served API that is deprecated or removed by the
// target version.
type UpgradeFinding struct {
	Type         UpgradeIssueType `json:"type"`
	Severity     Severity         `json:"severity"`
	APIVersion   string           `json:"apiVersion"`
	Kind         string           `json:"kind"`
	DeprecatedIn string           `json:"deprecatedIn,omitempty"`
	RemovedIn    string           `json:"removedIn,omitempty"`
	Replacement  string           `json:"replacement,omitempty"`
	Instances    int              `json:"instances,omitempty"`
	Message      string           `json:"message"`
}

// UpgradeSummary is a quick-glance rollup.
type UpgradeSummary struct {
	RemovedAPIs       int  `json:"removedApis"`
	DeprecatedAPIs    int  `json:"deprecatedApis"`
	AffectedInstances int  `json:"affectedInstances"`
	TargetResolved    bool `json:"targetResolved"`
	UpgradeSafe       bool `json:"upgradeSafe"` // no removed APIs in use for the target
}

// UpgradeReport is the full result of an upgrade-readiness run.
type UpgradeReport struct {
	ClusterID      string           `json:"clusterId"`
	CurrentVersion string           `json:"currentVersion"`
	TargetVersion  string           `json:"targetVersion"`
	GeneratedAt    time.Time        `json:"generatedAt"`
	Summary        UpgradeSummary   `json:"summary"`
	Findings       []UpgradeFinding `json:"findings"`
}

// AnalyzeUpgrade compares the APIs a cluster serves against the deprecation
// registry for a target version. If requestedTarget is empty it defaults to the
// next minor after the cluster's current version. It performs no I/O.
func AnalyzeUpgrade(snap UpgradeSnapshot, requestedTarget string) UpgradeReport {
	target, targetStr, resolved := resolveTarget(snap.CurrentVersion, requestedTarget)

	report := UpgradeReport{
		ClusterID:      snap.ClusterID,
		CurrentVersion: snap.CurrentVersion,
		TargetVersion:  targetStr,
		GeneratedAt:    time.Now().UTC(),
		Findings:       make([]UpgradeFinding, 0),
	}
	report.Summary.TargetResolved = resolved
	if !resolved {
		report.Summary.UpgradeSafe = true // nothing to compare against
		return report
	}

	for _, s := range snap.ServedAPIs {
		reg, ok := registryIndex[gvk{s.Group, s.Version, s.Kind}]
		if !ok {
			continue
		}
		removed := reg.RemovedIn != "" && versionAtLeast(target, reg.RemovedIn)
		deprecated := reg.DeprecatedIn != "" && versionAtLeast(target, reg.DeprecatedIn)

		var f UpgradeFinding
		switch {
		case removed:
			f = UpgradeFinding{
				Type: IssueRemovedAPI, Severity: SeverityCritical,
				Message: fmt.Sprintf("%s %s is removed in %s; migrate before upgrading", reg.apiVersion(), reg.Kind, reg.RemovedIn),
			}
			report.Summary.RemovedAPIs++
		case deprecated:
			f = UpgradeFinding{
				Type: IssueDeprecatedAPI, Severity: SeverityWarning,
				Message: fmt.Sprintf("%s %s is deprecated as of %s; plan migration", reg.apiVersion(), reg.Kind, reg.DeprecatedIn),
			}
			report.Summary.DeprecatedAPIs++
		default:
			continue // served and registered, but still fine at the target version
		}

		f.APIVersion = reg.apiVersion()
		f.Kind = reg.Kind
		f.DeprecatedIn = reg.DeprecatedIn
		f.RemovedIn = reg.RemovedIn
		f.Replacement = reg.Replacement
		if s.InstancesKnown {
			f.Instances = s.InstanceCount
			report.Summary.AffectedInstances += s.InstanceCount
		}
		report.Findings = append(report.Findings, f)
	}

	report.Summary.UpgradeSafe = report.Summary.RemovedAPIs == 0
	sortUpgradeFindings(report.Findings)
	return report
}

// resolveTarget returns the target version to compare against. An explicit
// request wins; otherwise default to the next minor after current.
func resolveTarget(current, requested string) (parsedVersion, string, bool) {
	if requested != "" {
		if v, ok := parseVersion(requested); ok {
			return v, fmt.Sprintf("%d.%d", v.major, v.minor), true
		}
		return parsedVersion{}, "", false
	}
	if v, ok := parseVersion(current); ok {
		next := parsedVersion{major: v.major, minor: v.minor + 1}
		return next, fmt.Sprintf("%d.%d", next.major, next.minor), true
	}
	return parsedVersion{}, "", false
}

type parsedVersion struct{ major, minor int }

// parseVersion parses "v1.24.7", "1.24", "1.24.0" into major/minor.
func parseVersion(s string) (parsedVersion, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	// Trim GKE/EKS-style suffixes like "1.27.3-gke.100".
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return parsedVersion{}, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return parsedVersion{}, false
	}
	return parsedVersion{major, minor}, true
}

// versionAtLeast reports whether target >= the given minor-version string.
func versionAtLeast(target parsedVersion, other string) bool {
	o, ok := parseVersion(other)
	if !ok {
		return false
	}
	if target.major != o.major {
		return target.major > o.major
	}
	return target.minor >= o.minor
}

func sortUpgradeFindings(f []UpgradeFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if r := severityRank(a.Severity) - severityRank(b.Severity); r != 0 {
			return r < 0
		}
		if a.APIVersion != b.APIVersion {
			return a.APIVersion < b.APIVersion
		}
		return a.Kind < b.Kind
	})
}
