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

An unreachable cluster is a **finding**, not a transport error: the endpoint
returns `200` with a low-scoring report. A `503` means no kubeconfig was
configured at all.

## Roadmap

Workload analysis, resource optimization, reliability/upgrade/GitOps/security
checks, capacity planning, the AI explanation layer, and production hardening
(Helm, full Prom/Grafana/OTel, CI) land in subsequent milestones. See
`docs/roadmap.md` (added later).
