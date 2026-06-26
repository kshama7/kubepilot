# Engineering Tradeoffs

Decisions made building KubePilot, with the reasoning and the cost of each.
This is the document to read before asking "why didn't you just…".

## Deterministic rules first, AI second

**Decision:** Every finding and score is produced by deterministic Go rules. The
Claude API layer (M9) only *explains* findings that already exist.

**Why:** Reliability tooling has to be auditable. An on-call engineer at 3am must
be able to trust the score and trace exactly which rule fired. "The model flagged
it" is not an answer during an incident. Rules are unit-tested; AI output is not
load-bearing.

**Cost:** We maintain rule logic by hand and curate data (e.g. the deprecation
registry) instead of asking a model to infer it. Worth it for trust and testability.

## Collection and scoring are separate packages

**Decision:** `internal/k8s` does all I/O and converts API objects into plain
structs; `internal/analysis` is pure functions over those structs and imports no
client-go.

**Why:** The entire rule engine is covered by table tests with hand-built
snapshots — no kind cluster, no envtest, no fakes in CI. It also keeps the rules
readable in isolation.

**Cost:** Some duplication between the API types and the analysis structs, and a
translation layer in the collector. Cheap relative to the testability win.

## ArgoCD via the dynamic client, not the argo-cd Go module

**Decision:** Read ArgoCD `Application` custom resources with client-go's dynamic
client and `unstructured`, rather than importing `github.com/argoproj/argo-cd`.

**Why:** The argo-cd module pulls an enormous, frequently-conflicting transitive
dependency tree (it vendors much of Kubernetes itself) and is notoriously painful
to build as a library. We only need a handful of `status` fields — sync, health,
operation phase, per-resource drift — which `unstructured.NestedString` reads
directly. This is how most external integrations actually consume ArgoCD.

**Cost:** No compile-time typing on the Application schema; we depend on field
paths staying stable across ArgoCD versions (they have, for `v1alpha1`). The
parser tolerates any missing sub-object, so a schema gap degrades to empty fields
rather than a crash.

## Rightsizing from point-in-time metrics, flagged as such

**Decision:** Resource rightsizing (M3) uses a live metrics-server snapshot and
labels every recommendation "validate against historical peak".

**Why:** metrics-server gives instantaneous usage, not history. Honest point-in-
time advice beats either fabricating trend data or shipping nothing. True
historical rightsizing belongs with the Prometheus-backed capacity work (M8).

**Cost:** Recommendations need human validation before applying. We make that
explicit in every finding rather than implying false confidence.

## Boot without a cluster; failures are findings

**Decision:** The API starts even with no reachable cluster. Cluster-health
reports an unreachable API server as a low-scoring *finding* (HTTP 200); analyzers
that need object data return 502.

**Why:** A monitoring tool that crashes when its target is down is a second
outage. And "is the control plane reachable?" is itself the question cluster
health exists to answer.

**Cost:** Slightly more nuanced status-code semantics per endpoint, documented in
`architecture.md`.
