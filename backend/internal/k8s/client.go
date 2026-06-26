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

	"github.com/kshama7/kubepilot/backend/internal/analysis"
)

// Client is a thin, logged wrapper over a Kubernetes clientset.
type Client struct {
	clientset kubernetes.Interface
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
	log.Info("kubernetes client initialized", zap.String("config_source", source), zap.String("host", cfg.Host))
	return &Client{clientset: cs, log: log}, nil
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
