"use client";

import { useCallback, useEffect, useState } from "react";
import { AnalyzerKey, analyzerByKey } from "@/lib/analyzers";
import {
  FetchResult,
  countBySeverity,
  fetchAnalysis,
  normalizeFindings,
  sortBySeverity,
} from "@/lib/api";
import { AnalysisReport } from "@/lib/types";
import { useCluster } from "./ClusterContext";
import { ExplainPanel } from "./ExplainPanel";
import { FindingsTable } from "./FindingsTable";
import { Panel, Stat } from "./Panel";
import { PageHeader, RefreshButton } from "./PageHeader";
import { ErrorState, Loading } from "./States";

export function AnalysisView({ analyzer }: { analyzer: AnalyzerKey }) {
  const meta = analyzerByKey(analyzer);
  const { clusterId, namespace, target } = useCluster();
  const [result, setResult] = useState<FetchResult<AnalysisReport> | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(() => {
    setLoading(true);
    fetchAnalysis(meta.endpoint, clusterId, {
      namespace: meta.needsNamespace ? namespace : undefined,
      target: analyzer === "upgrade" ? target : undefined,
    }).then((r) => {
      setResult(r);
      setLoading(false);
    });
  }, [meta.endpoint, meta.needsNamespace, analyzer, clusterId, namespace, target]);

  useEffect(() => {
    load();
  }, [load]);

  const report = result?.data;
  const findings = report
    ? sortBySeverity(normalizeFindings(analyzer, report))
    : [];
  const counts = countBySeverity(findings);

  return (
    <div>
      <PageHeader
        title={meta.label}
        subtitle={meta.question}
        right={<RefreshButton onClick={load} loading={loading} />}
      />

      {loading && !report && <Loading what={meta.label.toLowerCase()} />}
      {result?.error && <ErrorState status={result.status} error={result.error} />}

      {report && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-6">
            <Stat
              label="critical"
              value={counts.critical}
              accent={counts.critical ? "text-sev-critical" : "text-fg-muted"}
            />
            <Stat
              label="warning"
              value={counts.warning}
              accent={counts.warning ? "text-sev-warning" : "text-fg-muted"}
            />
            <Stat
              label="info"
              value={counts.info}
              accent={counts.info ? "text-sev-info" : "text-fg-muted"}
            />
            {summaryStats(report.summary)}
          </div>

          <Panel title={`Findings (${findings.length})`}>
            <FindingsTable findings={findings} />
          </Panel>

          {findings.length > 0 && <ExplainPanel analyzer={analyzer} />}
        </div>
      )}
    </div>
  );
}

// summaryStats renders the report's primitive summary fields as chips, skipping
// the by-severity/by-type maps (shown via the severity counts above).
function summaryStats(summary?: Record<string, unknown>) {
  if (!summary) return null;
  const skip = new Set([
    "findingsBySeverity",
    "findingsByType",
  ]);
  return Object.entries(summary)
    .filter(([k, v]) => !skip.has(k) && (typeof v !== "object" || v === null))
    .slice(0, 9)
    .map(([k, v]) => (
      <Stat key={k} label={humanize(k)} value={formatVal(v)} />
    ));
}

function humanize(key: string): string {
  return key
    .replace(/([A-Z])/g, " $1")
    .replace(/^./, (c) => c.toUpperCase())
    .replace(/\bPdb\b/, "PDB")
    .replace(/\bCpu\b/, "CPU")
    .trim();
}

function formatVal(v: unknown): string {
  if (typeof v === "boolean") return v ? "yes" : "no";
  if (typeof v === "number") return Intl.NumberFormat().format(v);
  return String(v ?? "—");
}
