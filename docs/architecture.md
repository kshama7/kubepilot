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

For cluster health, an unreachable cluster is a **finding**: the endpoint
returns `200` with a low-scoring report. The workload endpoint instead returns
`502` when pods cannot be listed — without pod data there is nothing to analyze.
Both return `503` when no kubeconfig was configured at all.

## Roadmap

Workload analysis, resource optimization, reliability/upgrade/GitOps/security
checks, capacity planning, the AI explanation layer, and production hardening
(Helm, full Prom/Grafana/OTel, CI) land in subsequent milestones. See
`docs/roadmap.md` (added later).
