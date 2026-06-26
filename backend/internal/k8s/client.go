// Package k8s wraps client-go for KubePilot. It owns all I/O against the
// Kubernetes API and translates raw API objects into the plain snapshot structs
// the analysis package scores. The analyzers never import client-go.
package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/kshama7/kubepilot/backend/internal/analysis"
)

// argoApplicationGVR is the ArgoCD Application custom resource. KubePilot reads
// it via the dynamic client rather than vendoring the (very heavy) argo-cd Go
// module — see docs/tradeoffs.md.
var argoApplicationGVR = schema.GroupVersionResource{
	Group: "argoproj.io", Version: "v1alpha1", Resource: "applications",
}

// Client is a thin, logged wrapper over a Kubernetes clientset.
//
// metrics is optional: it talks to the metrics.k8s.io API (metrics-server). It
// is nil in tests and may be non-nil but unanswered when metrics-server is not
// installed, in which case usage-based analysis degrades gracefully.
type Client struct {
	clientset kubernetes.Interface
	metrics   metricsclient.Interface
	dynamic   dynamic.Interface
	log       *zap.Logger
}

// NewClient builds a Client. Resolution order:
//  1. in-cluster config (when running inside a pod)
//  2. explicit kubeconfigPath, if provided
//  3. default kubeconfig loading rules (KUBECONFIG, then ~/.kube/config)
//
// A non-nil error means we could not even construct a REST config; it does not
// imply the API server is reachable. Reachability is checked at analysis time.
func NewClient(log *zap.Logger, kubeconfigPath string) (*Client, error) {
	cfg, source, err := restConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}
	// Keep the wrapper responsive; analysis endpoints must fail fast, not hang.
	cfg.Timeout = 10 * time.Second

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	// The metrics clientset shares the same REST config. Constructing it never
	// hits the network; whether metrics-server actually answers is determined at
	// collection time.
	mc, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build metrics clientset: %w", err)
	}
	// The dynamic client lets the upgrade analyzer count instances of arbitrary
	// (including deprecated) GVRs without compiled-in typed clients.
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	log.Info("kubernetes client initialized", zap.String("config_source", source), zap.String("host", cfg.Host))
	return &Client{clientset: cs, metrics: mc, dynamic: dyn, log: log}, nil
}

// NewClientFromInterface wraps an existing clientset. Used in tests with a fake.
func NewClientFromInterface(log *zap.Logger, cs kubernetes.Interface) *Client {
	return &Client{clientset: cs, log: log}
}

func restConfig(kubeconfigPath string) (*rest.Config, string, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, "in-cluster", nil
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, "", err
	}
	source := "kubeconfig"
	if kubeconfigPath != "" {
		source = "kubeconfig:" + kubeconfigPath
	}
	return cfg, source, nil
}

// CollectClusterSnapshot gathers the state the cluster-health rules evaluate:
// API server reachability/version and per-node conditions. It never returns an
// error for an unreachable cluster — that is a finding, recorded on the
// snapshot, not a transport failure.
func (c *Client) CollectClusterSnapshot(ctx context.Context, clusterID string) analysis.ClusterSnapshot {
	snap := analysis.ClusterSnapshot{
		ClusterID:   clusterID,
		CollectedAt: time.Now().UTC(),
	}

	version, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		c.log.Warn("api server unreachable", zap.String("cluster_id", clusterID), zap.Error(err))
		snap.APIServerReachable = false
		snap.APIServerError = err.Error()
		return snap
	}
	snap.APIServerReachable = true
	snap.ServerVersion = version.GitVersion

	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		// Control plane answered version but node listing failed (e.g. RBAC).
		// Record it; readiness scoring will treat zero nodes as critical.
		c.log.Warn("listing nodes failed", zap.String("cluster_id", clusterID), zap.Error(err))
		snap.APIServerError = fmt.Sprintf("nodes list failed: %v", err)
		return snap
	}

	snap.Nodes = make([]analysis.NodeStatus, 0, len(nodeList.Items))
	for i := range nodeList.Items {
		snap.Nodes = append(snap.Nodes, nodeStatusFrom(&nodeList.Items[i]))
	}
	return snap
}

// CollectWorkloadSnapshot lists pods (in the given namespace, or all namespaces
// when namespace is "") and distills them into the structs the workload rules
// evaluate. As with cluster health, an unreachable API server is recorded on
// the snapshot rather than returned as an error.
func (c *Client) CollectWorkloadSnapshot(ctx context.Context, clusterID, namespace string) analysis.WorkloadSnapshot {
	snap := analysis.WorkloadSnapshot{
		ClusterID:   clusterID,
		Namespace:   namespace,
		CollectedAt: time.Now().UTC(),
	}

	podList, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.log.Warn("listing pods failed",
			zap.String("cluster_id", clusterID),
			zap.String("namespace", namespace),
			zap.Error(err))
		snap.APIServerReachable = false
		snap.APIServerError = err.Error()
		return snap
	}
	snap.APIServerReachable = true

	snap.Pods = make([]analysis.PodStatus, 0, len(podList.Items))
	for i := range podList.Items {
		snap.Pods = append(snap.Pods, podStatusFrom(&podList.Items[i]))
	}
	return snap
}

// podStatusFrom distills a corev1.Pod into the workload scoring fields.
func podStatusFrom(p *corev1.Pod) analysis.PodStatus {
	ps := analysis.PodStatus{
		Namespace: p.Namespace,
		Name:      p.Name,
		Phase:     string(p.Status.Phase),
		NodeName:  p.Spec.NodeName,
		Scheduled: true,
		CreatedAt: p.CreationTimestamp.Time,
	}

	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status != corev1.ConditionTrue {
			ps.Scheduled = false
			ps.UnschedulableReason = cond.Reason
			ps.UnschedulableMessage = cond.Message
		}
	}

	for i := range p.Status.InitContainerStatuses {
		ps.Containers = append(ps.Containers, containerStateFrom(&p.Status.InitContainerStatuses[i], true))
	}
	for i := range p.Status.ContainerStatuses {
		ps.Containers = append(ps.Containers, containerStateFrom(&p.Status.ContainerStatuses[i], false))
	}
	return ps
}

func containerStateFrom(cs *corev1.ContainerStatus, init bool) analysis.ContainerState {
	out := analysis.ContainerState{
		Name:         cs.Name,
		Ready:        cs.Ready,
		RestartCount: cs.RestartCount,
		Init:         init,
	}
	if cs.Started != nil {
		out.Started = *cs.Started
	}
	if cs.State.Waiting != nil {
		out.WaitingReason = cs.State.Waiting.Reason
		out.WaitingMessage = cs.State.Waiting.Message
	}
	if cs.LastTerminationState.Terminated != nil {
		out.LastTerminatedReason = cs.LastTerminationState.Terminated.Reason
		out.LastTerminatedExitCode = cs.LastTerminationState.Terminated.ExitCode
	}
	return out
}

// CollectResourceSnapshot lists pods (their specs carry requests/limits and
// status carries the QoS class) and, when metrics-server is reachable, merges in
// live per-container usage so the resource rules can produce rightsizing
// recommendations. Absent metrics-server, MetricsAvailable is false and the
// rules fall back to spec-only checks.
func (c *Client) CollectResourceSnapshot(ctx context.Context, clusterID, namespace string) analysis.ResourceSnapshot {
	snap := analysis.ResourceSnapshot{
		ClusterID:   clusterID,
		Namespace:   namespace,
		CollectedAt: time.Now().UTC(),
	}

	podList, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.log.Warn("listing pods failed",
			zap.String("cluster_id", clusterID), zap.String("namespace", namespace), zap.Error(err))
		snap.APIServerReachable = false
		snap.APIServerError = err.Error()
		return snap
	}
	snap.APIServerReachable = true

	usage := c.collectPodUsage(ctx, namespace)
	snap.MetricsAvailable = usage != nil

	snap.Pods = make([]analysis.PodResources, 0, len(podList.Items))
	for i := range podList.Items {
		snap.Pods = append(snap.Pods, podResourcesFrom(&podList.Items[i], usage))
	}
	return snap
}

// usageKey indexes live usage by namespace/pod/container.
type usageKey struct{ ns, pod, container string }
type usageVal struct{ cpuMilli, memBytes int64 }

// collectPodUsage queries metrics-server. A nil return means usage is
// unavailable (metrics-server absent or erroring) — the caller treats that as
// "spec-only analysis", not a failure.
func (c *Client) collectPodUsage(ctx context.Context, namespace string) map[usageKey]usageVal {
	if c.metrics == nil {
		return nil
	}
	list, err := c.metrics.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.log.Info("pod metrics unavailable; resource analysis will be spec-only",
			zap.String("namespace", namespace), zap.Error(err))
		return nil
	}
	out := make(map[usageKey]usageVal, len(list.Items))
	for _, pm := range list.Items {
		for _, cm := range pm.Containers {
			cpu := cm.Usage.Cpu()
			mem := cm.Usage.Memory()
			out[usageKey{pm.Namespace, pm.Name, cm.Name}] = usageVal{
				cpuMilli: cpu.MilliValue(),
				memBytes: mem.Value(),
			}
		}
	}
	return out
}

func podResourcesFrom(p *corev1.Pod, usage map[usageKey]usageVal) analysis.PodResources {
	pr := analysis.PodResources{
		Namespace: p.Namespace,
		Name:      p.Name,
		QOSClass:  string(p.Status.QOSClass),
	}
	add := func(c *corev1.Container, init bool) {
		cr := analysis.ContainerResources{
			Name:     c.Name,
			Init:     init,
			Requests: quantitiesFrom(c.Resources.Requests),
			Limits:   quantitiesFrom(c.Resources.Limits),
		}
		if usage != nil {
			if u, ok := usage[usageKey{p.Namespace, p.Name, c.Name}]; ok {
				cr.HasUsage = true
				cr.UsageCPUMilli = u.cpuMilli
				cr.UsageMemBytes = u.memBytes
			}
		}
		pr.Containers = append(pr.Containers, cr)
	}
	for i := range p.Spec.InitContainers {
		add(&p.Spec.InitContainers[i], true)
	}
	for i := range p.Spec.Containers {
		add(&p.Spec.Containers[i], false)
	}
	return pr
}

func quantitiesFrom(rl corev1.ResourceList) analysis.ResourceQuantities {
	q := analysis.ResourceQuantities{}
	if cpu, ok := rl[corev1.ResourceCPU]; ok {
		q.CPUMilli = cpu.MilliValue()
		q.CPUSet = true
	}
	if mem, ok := rl[corev1.ResourceMemory]; ok {
		q.MemBytes = mem.Value()
		q.MemSet = true
	}
	return q
}

// CollectReliabilitySnapshot lists Deployments and StatefulSets plus the
// namespace's PodDisruptionBudgets, then matches each PDB to a workload by its
// real label selector so the reliability rules can reason about PDB coverage.
func (c *Client) CollectReliabilitySnapshot(ctx context.Context, clusterID, namespace string) analysis.ReliabilitySnapshot {
	snap := analysis.ReliabilitySnapshot{
		ClusterID:   clusterID,
		Namespace:   namespace,
		CollectedAt: time.Now().UTC(),
	}

	deploys, err := c.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.log.Warn("listing deployments failed", zap.String("cluster_id", clusterID), zap.Error(err))
		snap.APIServerReachable = false
		snap.APIServerError = err.Error()
		return snap
	}
	statefulsets, err := c.clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.log.Warn("listing statefulsets failed", zap.String("cluster_id", clusterID), zap.Error(err))
		snap.APIServerReachable = false
		snap.APIServerError = err.Error()
		return snap
	}
	snap.APIServerReachable = true

	// PDBs are optional (the policy API or RBAC may be unavailable); treat a
	// failure as "no PDBs" rather than failing the whole analysis.
	var pdbs []policyv1.PodDisruptionBudget
	if pdbList, err := c.clientset.PolicyV1().PodDisruptionBudgets(namespace).List(ctx, metav1.ListOptions{}); err != nil {
		c.log.Info("listing PDBs failed; treating workloads as unprotected", zap.Error(err))
	} else {
		pdbs = pdbList.Items
	}

	for i := range deploys.Items {
		d := &deploys.Items[i]
		snap.Workloads = append(snap.Workloads, workloadSpecFrom(
			d.Namespace, d.Name, "Deployment", replicasOrDefault(d.Spec.Replicas),
			d.Spec.Template, pdbs))
	}
	for i := range statefulsets.Items {
		s := &statefulsets.Items[i]
		snap.Workloads = append(snap.Workloads, workloadSpecFrom(
			s.Namespace, s.Name, "StatefulSet", replicasOrDefault(s.Spec.Replicas),
			s.Spec.Template, pdbs))
	}
	return snap
}

func replicasOrDefault(r *int32) int32 {
	if r == nil {
		return 1 // apps/v1 defaults a nil replica count to 1
	}
	return *r
}

func workloadSpecFrom(ns, name, kind string, replicas int32, tmpl corev1.PodTemplateSpec, pdbs []policyv1.PodDisruptionBudget) analysis.WorkloadSpec {
	w := analysis.WorkloadSpec{
		Namespace: ns, Name: name, Kind: kind, Replicas: replicas,
		HasPodAntiAffinity: tmpl.Spec.Affinity != nil && tmpl.Spec.Affinity.PodAntiAffinity != nil,
		HasTopologySpread:  len(tmpl.Spec.TopologySpreadConstraints) > 0,
	}
	for i := range tmpl.Spec.InitContainers {
		w.Containers = append(w.Containers, probesFrom(&tmpl.Spec.InitContainers[i], true))
	}
	for i := range tmpl.Spec.Containers {
		w.Containers = append(w.Containers, probesFrom(&tmpl.Spec.Containers[i], false))
	}
	w.PDBs = matchingPDBs(tmpl.Labels, replicas, pdbs)
	return w
}

func probesFrom(c *corev1.Container, init bool) analysis.ContainerProbes {
	return analysis.ContainerProbes{
		Name:         c.Name,
		Init:         init,
		HasReadiness: c.ReadinessProbe != nil,
		HasLiveness:  c.LivenessProbe != nil,
		HasStartup:   c.StartupProbe != nil,
	}
}

// matchingPDBs returns the PDBs whose selector matches the workload's pod-template
// labels, resolving whether each permits at least one voluntary disruption given
// the replica count.
func matchingPDBs(templateLabels map[string]string, replicas int32, pdbs []policyv1.PodDisruptionBudget) []analysis.PDBRef {
	if len(templateLabels) == 0 {
		return nil
	}
	set := labels.Set(templateLabels)
	var out []analysis.PDBRef
	for i := range pdbs {
		pdb := &pdbs[i]
		sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil || sel.Empty() || !sel.Matches(set) {
			continue
		}
		out = append(out, analysis.PDBRef{
			Name:             pdb.Name,
			MinAvailable:     intstrString(pdb.Spec.MinAvailable),
			MaxUnavailable:   intstrString(pdb.Spec.MaxUnavailable),
			AllowsDisruption: pdbAllowsDisruption(pdb, replicas),
		})
	}
	return out
}

// pdbAllowsDisruption reports whether the PDB leaves room to evict at least one
// pod, using the same int/percent rounding the disruption controller applies.
func pdbAllowsDisruption(pdb *policyv1.PodDisruptionBudget, replicas int32) bool {
	total := int(replicas)
	if pdb.Spec.MaxUnavailable != nil {
		maxUnavail, err := intstr.GetScaledValueFromIntOrPercent(pdb.Spec.MaxUnavailable, total, false)
		if err != nil {
			return true // can't resolve → don't raise a false alarm
		}
		return maxUnavail >= 1
	}
	if pdb.Spec.MinAvailable != nil {
		minAvail, err := intstr.GetScaledValueFromIntOrPercent(pdb.Spec.MinAvailable, total, true)
		if err != nil {
			return true
		}
		return total-minAvail >= 1
	}
	return true // neither field set: PDB imposes no constraint
}

func intstrString(v *intstr.IntOrString) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// CollectUpgradeSnapshot determines the cluster version and which registry-listed
// deprecated APIs the cluster currently serves, counting live instances of each
// via the dynamic client. The version comparison itself is left to the analyzer.
func (c *Client) CollectUpgradeSnapshot(ctx context.Context, clusterID string) analysis.UpgradeSnapshot {
	snap := analysis.UpgradeSnapshot{
		ClusterID:   clusterID,
		CollectedAt: time.Now().UTC(),
	}

	version, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		c.log.Warn("api server unreachable", zap.String("cluster_id", clusterID), zap.Error(err))
		snap.APIServerReachable = false
		snap.APIServerError = err.Error()
		return snap
	}
	snap.APIServerReachable = true
	snap.CurrentVersion = version.GitVersion

	// ServerGroupsAndResources can return partial results with an aggregate error
	// (a flaky aggregated-API group should not blind the rest of the scan).
	_, resourceLists, err := c.clientset.Discovery().ServerGroupsAndResources()
	if err != nil {
		c.log.Info("partial API discovery", zap.String("cluster_id", clusterID), zap.Error(err))
	}

	// Index served resources (group/version/kind -> plural resource name).
	type served struct{ resource string }
	servedByGVK := map[schema.GroupVersionKind]served{}
	for _, rl := range resourceLists {
		gv, perr := schema.ParseGroupVersion(rl.GroupVersion)
		if perr != nil {
			continue
		}
		for _, r := range rl.APIResources {
			if strings.Contains(r.Name, "/") {
				continue // skip subresources like pods/status
			}
			servedByGVK[schema.GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: r.Kind}] = served{resource: r.Name}
		}
	}

	for _, d := range analysis.DeprecationRegistry() {
		key := schema.GroupVersionKind{Group: d.Group, Version: d.Version, Kind: d.Kind}
		s, ok := servedByGVK[key]
		if !ok {
			continue
		}
		api := analysis.ServedAPI{Group: d.Group, Version: d.Version, Kind: d.Kind}
		if count, known := c.countInstances(ctx, schema.GroupVersionResource{Group: d.Group, Version: d.Version, Resource: s.resource}); known {
			api.InstanceCount = count
			api.InstancesKnown = true
		}
		snap.ServedAPIs = append(snap.ServedAPIs, api)
	}
	return snap
}

// countInstances lists a GVR via the dynamic client and returns the instance
// count. A false second return means the count is unknown (listing failed).
func (c *Client) countInstances(ctx context.Context, gvr schema.GroupVersionResource) (int, bool) {
	if c.dynamic == nil {
		return 0, false
	}
	list, err := c.dynamic.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1000})
	if err != nil {
		c.log.Debug("counting instances failed", zap.String("gvr", gvr.String()), zap.Error(err))
		return 0, false
	}
	return len(list.Items), true
}

// CollectGitOpsSnapshot lists ArgoCD Application custom resources (in the given
// namespace, or cluster-wide when namespace is "") via the dynamic client. When
// the Application CRD is not installed the snapshot reports ArgoCDInstalled=false
// rather than treating it as an error — most clusters do not run ArgoCD.
func (c *Client) CollectGitOpsSnapshot(ctx context.Context, clusterID, namespace string) analysis.GitOpsSnapshot {
	snap := analysis.GitOpsSnapshot{
		ClusterID:   clusterID,
		Namespace:   namespace,
		CollectedAt: time.Now().UTC(),
	}

	// Confirm reachability first so we can distinguish "cluster down" from
	// "ArgoCD not installed".
	if _, err := c.clientset.Discovery().ServerVersion(); err != nil {
		c.log.Warn("api server unreachable", zap.String("cluster_id", clusterID), zap.Error(err))
		snap.APIServerReachable = false
		snap.APIServerError = err.Error()
		return snap
	}
	snap.APIServerReachable = true

	if c.dynamic == nil {
		return snap
	}
	list, err := c.dynamic.Resource(argoApplicationGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		// A 404 (CRD absent) or forbidden means no ArgoCD data to analyze.
		c.log.Info("ArgoCD Applications not listable; treating as not installed",
			zap.String("cluster_id", clusterID), zap.Error(err))
		return snap
	}
	snap.ArgoCDInstalled = true

	snap.Applications = make([]analysis.ArgoApplication, 0, len(list.Items))
	for i := range list.Items {
		snap.Applications = append(snap.Applications, argoApplicationFrom(&list.Items[i]))
	}
	return snap
}

// argoApplicationFrom distills an unstructured ArgoCD Application into the
// scoring-relevant fields, tolerating any missing status sub-objects.
func argoApplicationFrom(u *unstructured.Unstructured) analysis.ArgoApplication {
	app := analysis.ArgoApplication{
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
	}
	app.Project, _, _ = unstructured.NestedString(u.Object, "spec", "project")
	app.SyncStatus, _, _ = unstructured.NestedString(u.Object, "status", "sync", "status")
	app.HealthStatus, _, _ = unstructured.NestedString(u.Object, "status", "health", "status")
	app.OperationPhase, _, _ = unstructured.NestedString(u.Object, "status", "operationState", "phase")
	app.OperationMessage, _, _ = unstructured.NestedString(u.Object, "status", "operationState", "message")

	if conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions"); found {
		for _, c := range conds {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			t, _, _ := unstructured.NestedString(cm, "type")
			msg, _, _ := unstructured.NestedString(cm, "message")
			if t != "" {
				app.Conditions = append(app.Conditions, analysis.AppCondition{Type: t, Message: msg})
			}
		}
	}

	if resources, found, _ := unstructured.NestedSlice(u.Object, "status", "resources"); found {
		for _, r := range resources {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			rs := analysis.AppResourceStatus{}
			rs.Group, _, _ = unstructured.NestedString(rm, "group")
			rs.Kind, _, _ = unstructured.NestedString(rm, "kind")
			rs.Namespace, _, _ = unstructured.NestedString(rm, "namespace")
			rs.Name, _, _ = unstructured.NestedString(rm, "name")
			rs.SyncStatus, _, _ = unstructured.NestedString(rm, "status")
			rs.HealthStatus, _, _ = unstructured.NestedString(rm, "health", "status")
			app.Resources = append(app.Resources, rs)
		}
	}
	return app
}

// nodeStatusFrom distills a corev1.Node into the scoring-relevant fields.
func nodeStatusFrom(n *corev1.Node) analysis.NodeStatus {
	ns := analysis.NodeStatus{
		Name:           n.Name,
		Schedulable:    !n.Spec.Unschedulable,
		KubeletVersion: n.Status.NodeInfo.KubeletVersion,
	}
	for _, cond := range n.Status.Conditions {
		isTrue := cond.Status == corev1.ConditionTrue
		switch cond.Type {
		case corev1.NodeReady:
			ns.Ready = isTrue
		case corev1.NodeMemoryPressure:
			ns.MemoryPressure = isTrue
		case corev1.NodeDiskPressure:
			ns.DiskPressure = isTrue
		case corev1.NodePIDPressure:
			ns.PIDPressure = isTrue
		case corev1.NodeNetworkUnavailable:
			ns.NetworkUnavailable = isTrue
		}
	}
	if cpu := n.Status.Capacity.Cpu(); cpu != nil {
		ns.CPUCapacityMilli = cpu.MilliValue()
	}
	if mem := n.Status.Capacity.Memory(); mem != nil {
		ns.MemCapacityBytes = mem.Value()
	}
	return ns
}
