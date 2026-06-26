# Interview Guide

Talking points for discussing KubePilot in a platform-engineering or ML-infra
interview. Each is a real decision in the codebase, not a slogan.

## The thesis: deterministic-first, AI second

> "Reliability tooling has to be auditable. Every finding KubePilot reports comes
> from a deterministic Go rule I can point to and unit-test. The Claude layer only
> *explains* findings the rules already produced — it can't invent one. At 3am you
> need to trust the score, and 'the model said so' isn't an answer."

Backed by: the `internal/ai` system prompt forbids invention; the explain handler
passes only deterministic findings; a unit test asserts those findings appear in
the outgoing request body.

## Architecture: collection vs scoring

> "I split I/O from logic at the package boundary. `internal/k8s` does all the
> client-go work and distills API objects into plain structs; `internal/analysis`
> is pure functions over those structs. The payoff is that the entire rule engine
> is covered by table tests built from hand-made snapshots — no kind cluster, no
> envtest in CI. 70-plus tests run in under a second."

## Failures are findings, not errors

> "A monitoring tool that crashes when its target is down is a second outage. The
> API boots without a cluster; cluster-health reports an unreachable control plane
> as a *low-scoring finding* with HTTP 200, because 'is the control plane
> reachable?' is exactly the question that analyzer exists to answer. Analyzers
> that need object data return 502 instead. The status-code semantics are
> deliberate and documented per endpoint."

## Honest data boundaries

> "Resource rightsizing uses a metrics-server snapshot and labels every
> recommendation 'point-in-time — validate against historical peak.' Capacity
> saturation prediction uses Prometheus range data and does the least-squares fit
> in a pure, unit-tested function. When the data source is absent, the feature
> degrades — it never fabricates a trend. I'd rather ship honest partial value
> than fake completeness."

## A pragmatic dependency call

> "ArgoCD's Go module vendors half of Kubernetes and is painful to build as a
> library. I only needed a handful of `status` fields off the Application CR, so I
> read it with the dynamic client and `unstructured`. That's how most external
> integrations actually consume ArgoCD. It's written up in `docs/tradeoffs.md`."

## Observability as a first-class concern

> "Every analyzer exposes Prometheus metrics — analysis duration histograms,
> findings by severity, the health-score gauge, API latency by route. Every
> analysis run is an OpenTelemetry span tagged with cluster and analyzer. There's
> a Grafana dashboard and a Helm chart with read-only RBAC and a hardened security
> context. Both the metrics and the security context are things KubePilot's own
> analyzers would approve of."

## Questions to expect, and honest answers

- **"How do you know the rules are right?"** They encode well-known SRE and
  upstream Kubernetes facts (PDB drain math, API removal versions, Pod Security
  Standards), and each is pinned by a table test including a negative case.
- **"Why not just use kube-bench / pluto / goldilocks?"** Those are great; this
  unifies eight analysis domains behind one API with a consistent finding model
  and an explanation layer, which is the platform-team product, not the individual
  check.
- **"What would you build next?"** See [roadmap.md](roadmap.md) — historical
  rightsizing from Prometheus, a webhook/remediation loop, and the Next.js
  dashboard.
