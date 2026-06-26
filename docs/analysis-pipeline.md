# Analysis Pipeline

Every KubePilot endpoint follows the same four-stage pipeline. Understanding it
explains the whole codebase.

```
HTTP request ──▶ Collect ──▶ Score ──▶ Record ──▶ Respond
                   │           │          │
              internal/k8s  internal/   metrics +
              (I/O)         analysis    OTel span
                           (pure)
```

## 1. Collect (`internal/k8s`)

A collector method (`Collect*Snapshot`) does all the I/O: it talks to the
Kubernetes API (and optionally metrics-server, the dynamic client, or
Prometheus) and **distills the raw API objects into plain snapshot structs**.
The snapshot types live in `internal/analysis` and contain only the fields the
rules need.

Two rules hold here:

- **Reachability is data, not an exception.** An unreachable API server or
  absent CRD is recorded on the snapshot (`APIServerReachable`,
  `ArgoCDInstalled`, `MetricsAvailable`, `PrometheusAvailable`) rather than
  returned as an error. The handler decides the HTTP status from that.
- **No analysis happens here.** The collector never decides whether something is
  a problem — it only observes.

## 2. Score (`internal/analysis`)

A pure function (`Score*` / `Analyze*`) takes the snapshot and returns a report.
It performs no I/O, imports no client-go, and is deterministic. This is the
entire reason collection and scoring are separate packages: the rule engine is
covered by table tests built from hand-constructed snapshots — no cluster, no
fakes, no envtest.

Reports come in two shapes:

- **Scored** (cluster health) — a single 0–100 number with weighted checks.
- **Findings** (everything else) — a severity-sorted list, because workloads,
  resources, and security issues fail independently and get triaged one at a time.

## 3. Record (`internal/metrics` + OTel)

Each run records:

- `kubepilot_analysis_duration_seconds{analyzer,outcome}` — how long it took.
- `kubepilot_cluster_health_score{cluster_id}` — the latest score.
- `kubepilot_recommendations_total{analyzer,severity}` — findings emitted.
- The enclosing OpenTelemetry span is tagged with cluster id, analyzer, route,
  and status, so every analysis run is individually traceable.

## 4. Respond

The handler serializes the report as JSON. Status-code semantics are deliberate
and documented per endpoint in [architecture.md](architecture.md):

| Situation                                   | Status |
|---------------------------------------------|--------|
| Analysis succeeded                          | 200    |
| Cluster unreachable (cluster-health only)   | 200 (low score, it's a finding) |
| Data source unreachable (other analyzers)   | 502    |
| No kubeconfig configured at all             | 503    |
| AI requested but no API key                 | 503    |

## The AI detour (explain endpoint)

`/explain?analyzer=X` runs stages 1–2 for the named analyzer, reduces the report
to `[]ai.Finding`, and hands *only those findings* to Claude. The AI is a fifth,
optional stage that consumes deterministic output — it never re-enters stages 1–2
on its own. See [rule-engine.md](rule-engine.md) for why that ordering is
non-negotiable.
