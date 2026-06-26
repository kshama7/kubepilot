import { AnalyzerKey } from "./analyzers";
import { AnalysisReport, NormalizedFinding, RawFinding, Severity } from "./types";

// Result of a fetch: either data, a "service unavailable" signal (e.g. AI not
// configured, no kubeconfig), or an error message.
export interface FetchResult<T> {
  data?: T;
  status: number;
  error?: string;
}

export async function apiGet<T>(path: string): Promise<FetchResult<T>> {
  try {
    const res = await fetch(path, { cache: "no-store" });
    const status = res.status;
    let body: unknown = null;
    try {
      body = await res.json();
    } catch {
      // non-JSON response (e.g. proxy error) — fall through
    }
    if (!res.ok) {
      const err =
        body && typeof body === "object" && "error" in body
          ? String((body as { error: unknown }).error)
          : `request failed with status ${status}`;
      return { status, error: err };
    }
    return { status, data: body as T };
  } catch (e) {
    return { status: 0, error: e instanceof Error ? e.message : "network error" };
  }
}

interface QueryOpts {
  namespace?: string;
  target?: string;
}

export function analysisPath(
  endpoint: string,
  clusterId: string,
  opts: QueryOpts = {},
): string {
  const params = new URLSearchParams();
  if (opts.namespace) params.set("namespace", opts.namespace);
  if (opts.target) params.set("target", opts.target);
  const qs = params.toString();
  return `/api/v1/clusters/${encodeURIComponent(endpoint === "" ? clusterId : clusterId)}/${endpoint}${qs ? `?${qs}` : ""}`;
}

export function fetchAnalysis(
  endpoint: string,
  clusterId: string,
  opts: QueryOpts = {},
): Promise<FetchResult<AnalysisReport>> {
  return apiGet<AnalysisReport>(analysisPath(endpoint, clusterId, opts));
}

// normalizeFindings reduces an analyzer report to the table's flat finding shape,
// handling each analyzer's resource-field layout.
export function normalizeFindings(
  key: AnalyzerKey,
  report: AnalysisReport,
): NormalizedFinding[] {
  if (key === "cluster_health") {
    return (report.checks ?? [])
      .filter((c) => !c.passed)
      .map((c) => ({
        type: c.id,
        severity: c.severity,
        resource: report.clusterId ?? "(cluster)",
        message: c.message,
      }));
  }

  const findings = report.findings ?? [];
  return findings.map((f) => ({
    type: f.type,
    severity: f.severity,
    resource: resourceLabel(key, f),
    message: f.message,
  }));
}

function resourceLabel(key: AnalyzerKey, f: RawFinding): string {
  const join = (...parts: (string | undefined)[]) =>
    parts.filter(Boolean).join("/");
  switch (key) {
    case "workload":
    case "resource":
    case "security":
      return join(f.namespace, f.pod, f.container) || "(cluster)";
    case "reliability":
      return join(f.namespace, f.kind ? `${f.kind}/${f.workload}` : f.workload, f.container);
    case "gitops":
      return join(f.namespace, f.application);
    case "upgrade":
      return join(f.apiVersion, f.kind);
    case "capacity":
      return f.node ?? "(node)";
    default:
      return "(cluster)";
  }
}

const sevRank: Record<Severity, number> = { critical: 0, warning: 1, info: 2 };

export function sortBySeverity(findings: NormalizedFinding[]): NormalizedFinding[] {
  return [...findings].sort((a, b) => {
    const r = sevRank[a.severity] - sevRank[b.severity];
    if (r !== 0) return r;
    if (a.resource !== b.resource) return a.resource.localeCompare(b.resource);
    return a.type.localeCompare(b.type);
  });
}

export function countBySeverity(
  findings: NormalizedFinding[],
): Record<Severity, number> {
  const out: Record<Severity, number> = { critical: 0, warning: 0, info: 0 };
  for (const f of findings) out[f.severity]++;
  return out;
}
