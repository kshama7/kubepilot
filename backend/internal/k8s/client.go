// Package k8s wraps client-go for KubePilot. It owns all I/O against the
// Kubernetes API and translates raw API objects into the plain snapshot structs
// the analysis package scores. The analyzers never import client-go.
package k8s

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/kshama7/kubepilot/backend/internal/analysis"
)

// Client is a thin, logged wrapper over a Kubernetes clientset.
//
// metrics is optional: it talks to the metrics.k8s.io API (metrics-server). It
// is nil in tests and may be non-nil but unanswered when metrics-server is not
// installed, in which case usage-based analysis degrades gracefully.
type Client struct {
	clientset kubernetes.Interface
	metrics   metricsclient.Interface
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
	log.Info("kubernetes client initialized", zap.String("config_source", source), zap.String("host", cfg.Host))
	return &Client{clientset: cs, metrics: mc, log: log}, nil
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
