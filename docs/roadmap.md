# Roadmap

KubePilot's ten build milestones are complete: eight deterministic analyzers, the
AI explanation layer, and production hardening (Helm, Prometheus/Grafana, OTel,
CI). What follows is honest forward-looking work, not vaporware — each item has a
clear reason and a rough shape.

## Near term

- **Next.js dashboard.** The backend already serves every analyzer as JSON; the
  dashboard is the remaining surface. Dark, terminal-adjacent, monospace data,
  severity color-coding. Pages map 1:1 to the analyzers plus an Overview.
- **Historical rightsizing.** M3 rightsizing is point-in-time from metrics-server.
  Replace it with Prometheus range queries over a lookback window and recommend
  against the observed peak, not a single sample — the honest version M3
  deliberately deferred.
- **Multi-cluster.** The API is already keyed by `{id}`; wire a cluster registry
  (a set of kubeconfigs / in-cluster contexts) behind it so one KubePilot watches
  a fleet.

## Medium term

- **Findings persistence + trends.** Store finding history so the dashboard can
  show "new since last scan" and severity trends over time, not just a point read.
- **Remediation suggestions as diffs.** For deterministic fixes (add a readiness
  probe, set a memory limit, migrate a deprecated API), emit a ready-to-apply YAML
  patch alongside the finding.
- **Webhook / alerting integration.** Push critical findings to Slack/PagerDuty
  via Alertmanager, driven by the existing `kubepilot_recommendations_total`
  metric.

## Longer term

- **Policy as configuration.** Let operators tune thresholds and disable rules per
  namespace via a CRD or config map, without recompiling.
- **Admission-time checks.** Reuse the security and reliability rules in a
  validating admission webhook to catch issues before they reach the cluster.
- **Cost attribution.** Combine resource commitment with node cost data to put a
  dollar figure on over-provisioning findings.

## Explicit non-goals

- **AI that generates findings.** The deterministic-first contract is permanent.
  The AI layer will gain better explanations and remediation prose, never the
  authority to invent issues.
- **Mutating the cluster automatically.** KubePilot reads and recommends. Any
  apply step stays opt-in and human-reviewed.
