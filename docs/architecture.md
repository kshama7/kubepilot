# KubePilot Architecture

> Draft — evolves each milestone. This first cut covers the M1 scaffold and the
> Cluster Health analyzer.

## Design principle

KubePilot answers SRE questions with **deterministic, rule-based analysis**.
Rules produce findings and scores; the AI layer (a later milestone) only
*explains* findings that already exist. Nothing in a report is model-generated.

This shapes the package boundaries below: collection (I/O against Kubernetes),
scoring (pure functions), and serving (HTTP) are kept separate so the rules are
trivially testable without a cluster.

## Component flow

```
            ┌─────────────────────────────────────────────┐
            │                Next.js dashboard             │  (later milestone)
            └───────────────────────┬─────────────────────┘
                                    │ HTTP / JSON
                                    ▼
            ┌─────────────────────────────────────────────┐
            │            Go REST API  (chi router)         │
            │  internal/api  ·  Zap logs  ·  Prom metrics  │
            └───────────┬───────────────────┬─────────────┘
                        │                   │
          collect state │                   │ score state (pure)
                        ▼                   ▼
            ┌───────────────────┐   ┌─────────────────────┐
            │   internal/k8s    │   │  internal/analysis  │
            │ client-go wrapper │   │  rule evaluation    │
            └─────────┬─────────┘   └─────────────────────┘
                      │
                      ▼
            ┌───────────────────┐
            │  Kubernetes API   │
            └───────────────────┘
```

## Packages (M1)

| Package              | Responsibility                                                        |
|----------------------|-----------------------------------------------------------------------|
| `cmd/api`            | Process entrypoint: config, logger, graceful shutdown, container probe |
| `internal/config`    | Twelve-factor env configuration with defaults                         |
| `internal/k8s`       | client-go wrapper; turns Kubernetes objects into plain snapshot structs |
| `internal/analysis`  | Deterministic rule evaluation; **pure**, no I/O, fully unit-tested     |
| `internal/metrics`   | Prometheus collectors on a private registry                           |
| `internal/api`       | chi router, middleware, handlers orchestrating collect → score        |

The dependency arrow only ever points **k8s → analysis** (the collector builds
`analysis.ClusterSnapshot`). `analysis` imports nothing from `k8s` or client-go,
which is what keeps the scoring logic test-only and cluster-free.

## Cluster Health scoring (M1)

`analysis.ScoreClusterHealth` evaluates four weighted checks over a
`ClusterSnapshot`. Weights sum to 100, so a fully-failing cluster scores 0.

| Check                     | Weight | Penalty model                                  | Severity escalation              |
|---------------------------|:------:|------------------------------------------------|----------------------------------|
| API server reachability   |   35   | all-or-nothing                                 | critical on failure              |
| Node readiness            |   35   | proportional to fraction NotReady; 0 nodes = full | critical at ≥ ⅓ affected      |
| Node resource pressure    |   20   | proportional to fraction under pressure        | critical at ≥ ⅓ affected         |
| Node schedulability       |   10   | proportional to fraction cordoned              | capped at warning (planned drain)|

`score = clamp(100 − Σ penalties, 0, 100)`. Status: `healthy ≥ 90`,
`degraded ≥ 70`, else `critical`.

Resource pressure means any of the node conditions `MemoryPressure`,
`DiskPressure`, `PIDPressure`, or `NetworkUnavailable` being true.

### Why these weights

On-call reality drives the split: an unreachable control plane or unready nodes
are page-now events, so they dominate the score. A cordoned node is routine
during maintenance, so it is capped at warning and contributes little. The
proportional penalties mean "1 of 50 nodes NotReady" dents the score modestly,
while "20 of 50" pushes it into critical.

## Workload analysis (M2)

`analysis.AnalyzeWorkloads` evaluates a `WorkloadSnapshot` (pods, distilled from
the API) and emits per-pod/per-container `WorkloadFinding`s. Unlike cluster
health it produces a finding list, not a single score: workloads fail
independently, and an SRE triages them one at a time.

A single container can surface multiple findings on purpose — they answer
different questions:

| Issue type        | Detection rule                                              | Severity                        |
|-------------------|------------------------------------------------------------|---------------------------------|
| `CrashLoopBackOff`| container waiting reason is `CrashLoopBackOff`              | critical                        |
| `OOMKilled`       | last termination reason is `OOMKilled`                     | critical                        |
| `ImagePullError`  | waiting reason in {ImagePullBackOff, ErrImagePull, …}      | critical                        |
| `ContainerError`  | waiting reason in {CreateContainerError, RunContainerError, …} | critical                    |
| `Unschedulable`   | Pending + `PodScheduled=False`                             | critical                        |
| `PendingStuck`    | Pending past a 5-minute grace period                       | warning                         |
| `RestartStorm`    | restart count ≥ 5 (≥ 20 → critical)                        | warning → critical              |
| `NotReady`        | Running + started + not Ready (readiness probe failing)    | warning                         |
| `Failed`          | pod phase is `Failed`                                       | critical                        |
| `UnknownPhase`    | pod phase is `Unknown` (node unreachable)                  | warning                         |

Findings sort critical-first, then by namespace/pod/type. The `NotReady` rule is
suppressed while a container is crashlooping (the crashloop is the real story).
A fresh Pending pod within the grace period is not flagged, so normal
image-pull/sandbox-creation churn does not generate noise.

Collection vs scoring stays split exactly as in M1: `k8s.CollectWorkloadSnapshot`
does the listing, `analysis.AnalyzeWorkloads` is pure and table-tested.

## Resource optimization (M3)

`analysis.AnalyzeResources` evaluates a `ResourceSnapshot` (pod specs + optional
live usage). Spec-level rules run always; usage-based rightsizing runs only when
metrics-server answered (`MetricsAvailable`). No usage is ever fabricated.

| Issue type                 | Detection rule                                                  | Severity |
|----------------------------|----------------------------------------------------------------|----------|
| `BestEffortQoS`            | pod QoS is BestEffort (no requests/limits anywhere)            | warning  |
| `MissingCPURequest`        | container has no CPU request                                   | warning  |
| `MissingMemoryRequest`     | container has no memory request                               | warning  |
| `MissingMemoryLimit`       | container has no memory limit (node-OOM risk)                 | warning  |
| `HighLimitToRequestRatio`  | CPU limit ≥ 4× request (noisy-neighbor risk)                  | info     |
| `CPUOverProvisioned`       | live CPU usage < 30% of request (rightsizing candidate)       | info     |
| `MemoryOverProvisioned`    | live memory usage < 30% of request                           | info     |

A missing **CPU limit** is intentionally *not* flagged — it is acceptable and
often preferred (avoids CFS throttling). BestEffort pods collapse to a single
finding instead of a per-container storm. Rightsizing suggests
`ceil(usage × 1.2)` (20% headroom), only when it's below the current request and
above a floor (50m CPU / 64Mi), and labels every recommendation as point-in-time
— to be validated against historical peaks (the M8 capacity work). The report
sums reclaimable CPU/memory across all rightsizing findings.

Usage comes from the `metrics.k8s.io` API via a metrics-server clientset built
from the same REST config; when metrics-server is absent the analyzer degrades
to spec-only and sets `metricsAvailable: false`.

## Reliability checks (M4)

`analysis.AnalyzeReliability` evaluates a `ReliabilitySnapshot` of Deployments
and StatefulSets (with their matched PDBs) for redundancy and disruption-safety
gaps.

| Issue type                      | Detection rule                                                     | Severity |
|---------------------------------|-------------------------------------------------------------------|----------|
| `SingleReplica`                 | replicas == 1 (single point of failure)                           | warning  |
| `MissingPodDisruptionBudget`    | replicas > 1 and no PDB selects the pods                          | warning  |
| `PDBBlocksVoluntaryDisruption`  | a matching PDB permits zero disruptions (drains hang)            | warning  |
| `NoSpreadConstraints`           | replicas > 1 with neither pod anti-affinity nor topology spread  | warning  |
| `MissingReadinessProbe`         | container has no readiness probe                                 | warning  |
| `MissingLivenessProbe`          | container has no liveness probe                                  | info     |

**PDB matching is real, not heuristic.** The collector resolves each PDB's
`LabelSelector` (matchLabels *and* matchExpressions) against the workload's
pod-template labels via `metav1.LabelSelectorAsSelector`, and computes
`AllowsDisruption` with the same int/percent rounding the disruption controller
uses (`intstr.GetScaledValueFromIntOrPercent`). The rule layer only sees the
resolved booleans, so `analysis` stays free of apimachinery.

Single-replica workloads are not also nagged for a missing PDB or spread policy
(redundancy is the prerequisite). Init containers are skipped for probe checks
(they run to completion). A blocking PDB counts as coverage — it reports
`PDBBlocksVoluntaryDisruption`, never `MissingPodDisruptionBudget`.

## Observability (M1)

Exposed at `/metrics` on a private registry:

| Metric                                  | Type      | Labels                    |
|-----------------------------------------|-----------|---------------------------|
| `kubepilot_analysis_duration_seconds`   | histogram | `analyzer`, `outcome`     |
| `kubepilot_cluster_health_score`        | gauge     | `cluster_id`              |
| `kubepilot_recommendations_total`       | counter   | `analyzer`, `severity`    |
| `kubepilot_api_request_duration_seconds`| histogram | `method`, `route`, `status` |

## Endpoints (M1)

| Method | Path                                | Purpose                                |
|--------|-------------------------------------|----------------------------------------|
| GET    | `/healthz`                          | Liveness/readiness probe               |
| GET    | `/metrics`                          | Prometheus scrape                      |
| GET    | `/api/v1/clusters/{id}/health`      | Run the Cluster Health analyzer        |
| GET    | `/api/v1/clusters/{id}/workloads`   | Run the Workload analyzer (`?namespace=` optional) |
| GET    | `/api/v1/clusters/{id}/resources`   | Run the Resource analyzer (`?namespace=` optional) |
| GET    | `/api/v1/clusters/{id}/reliability` | Run the Reliability analyzer (`?namespace=` optional) |

For cluster health, an unreachable cluster is a **finding**: the endpoint
returns `200` with a low-scoring report. The workload endpoint instead returns
`502` when pods cannot be listed — without pod data there is nothing to analyze.
Both return `503` when no kubeconfig was configured at all.

## Roadmap

Workload analysis, resource optimization, reliability/upgrade/GitOps/security
checks, capacity planning, the AI explanation layer, and production hardening
(Helm, full Prom/Grafana/OTel, CI) land in subsequent milestones. See
`docs/roadmap.md` (added later).
