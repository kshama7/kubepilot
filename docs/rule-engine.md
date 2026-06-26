# Rule Engine

KubePilot's analysis is deterministic and rule-based. This document explains how
the rules are structured, why they're built this way, and how to add one.

## The contract

Every finding KubePilot reports is produced by a Go rule you can read, test, and
point to. There is no model in the analysis path. The AI layer (M9) only
*explains* findings the rules already produced — it cannot create one. This is
the single most important property of the system: an on-call engineer can trust
the output at 3am because they can trace exactly which rule fired and why.

## Anatomy of an analyzer

Each analyzer (`internal/analysis/*.go`) has the same shape:

1. **Snapshot types** — plain structs describing the observed state
   (`WorkloadSnapshot`, `SecuritySnapshot`, …). No client-go.
2. **Issue-type constants** — a closed enum of what this analyzer can find
   (`IssueCrashLoopBackOff`, `IssuePrivileged`, …).
3. **Thresholds as named constants** — every magic number is named and commented
   with the on-call reasoning (`restartStormThreshold = 5`,
   `overProvisionUsageRatio = 0.30`).
4. **A pure `Analyze*` / `Score*` function** — iterates the snapshot, applies the
   rules, returns a severity-sorted report.

## Severity model

Three levels, shared across all analyzers (`internal/analysis/cluster_health.go`):

| Severity   | Meaning                                              |
|------------|------------------------------------------------------|
| `critical` | Page-now: outage, data loss, or upgrade-breaking     |
| `warning`  | Reliability gap that should be fixed soon            |
| `info`     | Best-practice nudge; never drowns out a real problem |

Findings sort critical-first so the most urgent item is always at the top.

## Design rules the analyzers follow

These recur across every module and are worth internalizing:

- **Weights and thresholds encode on-call priority, not aesthetics.** Cluster
  health weights an unreachable control plane (35) far above a cordoned node (10)
  because that's what actually pages someone.
- **Suppress redundant findings.** A privileged container is the headline — it
  silences the `RunAsRoot`/`AllowPrivilegeEscalation` findings it would otherwise
  trigger. A crashlooping container suppresses the `NotReady` nag.
- **Don't manufacture data.** Rightsizing and saturation prediction only run when
  real usage exists (metrics-server / Prometheus); otherwise the relevant
  findings simply don't appear, and the report says so.
- **Failures are findings.** Absence (no PDB, no readiness probe, ArgoCD not
  installed) is reported as data, with the right severity — not as an error.

## Adding a rule

1. Add the issue-type constant and any threshold constants (commented).
2. Implement the check inside the analyzer's evaluation function, emitting a
   finding with the correct severity and a message an engineer can act on.
3. Add a table test in `*_test.go` with a hand-built snapshot covering the new
   case — including a negative case proving it does **not** fire when it
   shouldn't. No cluster required.
4. If the rule needs new observed state, add the field to the snapshot struct and
   populate it in the corresponding `internal/k8s` collector — never reach into
   client-go from the analyzer.

The test suite (`go test ./internal/analysis/`) is the specification. If a rule
isn't covered by a table test, it isn't done.
