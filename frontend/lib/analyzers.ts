// Analyzer catalog — the single source of truth the dashboard navigates by.

export type AnalyzerKey =
  | "cluster_health"
  | "workload"
  | "resource"
  | "reliability"
  | "upgrade"
  | "gitops"
  | "security"
  | "capacity";

export interface AnalyzerMeta {
  key: AnalyzerKey;
  label: string;
  // Backend path segment under /api/v1/clusters/{id}/...
  endpoint: string;
  // Dashboard route.
  href: string;
  question: string;
  needsNamespace: boolean;
  glyph: string; // monospace nav glyph
}

export const ANALYZERS: AnalyzerMeta[] = [
  {
    key: "cluster_health",
    label: "Cluster Health",
    endpoint: "health",
    href: "/clusters",
    question: "Is the control plane reachable and are nodes healthy?",
    needsNamespace: false,
    glyph: "◆",
  },
  {
    key: "workload",
    label: "Workloads",
    endpoint: "workloads",
    href: "/workloads",
    question: "What's crashlooping, OOMKilled, pending, or restart-storming?",
    needsNamespace: true,
    glyph: "▣",
  },
  {
    key: "resource",
    label: "Resources",
    endpoint: "resources",
    href: "/resources",
    question: "Where are we over-provisioned or missing requests/limits?",
    needsNamespace: true,
    glyph: "▤",
  },
  {
    key: "reliability",
    label: "Reliability",
    endpoint: "reliability",
    href: "/reliability",
    question: "Do workloads have PDBs, probes, replicas, anti-affinity?",
    needsNamespace: true,
    glyph: "▦",
  },
  {
    key: "upgrade",
    label: "Upgrade Advisor",
    endpoint: "upgrade",
    href: "/upgrade",
    question: "Which deprecated/removed APIs block the next version?",
    needsNamespace: false,
    glyph: "▲",
  },
  {
    key: "gitops",
    label: "GitOps",
    endpoint: "gitops",
    href: "/gitops",
    question: "What's drifted or out-of-sync in ArgoCD?",
    needsNamespace: true,
    glyph: "◈",
  },
  {
    key: "security",
    label: "Security",
    endpoint: "security",
    href: "/security",
    question: "Privileged containers, missing contexts, secrets in env?",
    needsNamespace: true,
    glyph: "⬡",
  },
  {
    key: "capacity",
    label: "Capacity",
    endpoint: "capacity",
    href: "/capacity",
    question: "Node utilization trends and saturation prediction.",
    needsNamespace: false,
    glyph: "▮",
  },
];

export function analyzerByKey(key: AnalyzerKey): AnalyzerMeta {
  return ANALYZERS.find((a) => a.key === key)!;
}
