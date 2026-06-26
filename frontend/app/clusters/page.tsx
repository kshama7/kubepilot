"use client";

import { useCallback, useEffect, useState } from "react";
import { useCluster } from "@/components/ClusterContext";
import { HealthGauge } from "@/components/HealthGauge";
import { Panel, Stat } from "@/components/Panel";
import { PageHeader, RefreshButton } from "@/components/PageHeader";
import { SeverityBadge } from "@/components/SeverityBadge";
import { ErrorState, Loading } from "@/components/States";
import { FetchResult, fetchAnalysis } from "@/lib/api";
import { AnalysisReport } from "@/lib/types";

export default function Page() {
  const { clusterId } = useCluster();
  const [result, setResult] = useState<FetchResult<AnalysisReport> | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(() => {
    setLoading(true);
    fetchAnalysis("health", clusterId).then((r) => {
      setResult(r);
      setLoading(false);
    });
  }, [clusterId]);

  useEffect(() => load(), [load]);

  const report = result?.data;
  return (
    <div>
      <PageHeader
        title="Cluster Health"
        subtitle={`Control plane reachability, node readiness, and resource pressure for "${clusterId}".`}
        right={<RefreshButton onClick={load} loading={loading} />}
      />

      {loading && !report && <Loading what="cluster health" />}
      {result?.error && <ErrorState status={result.status} error={result.error} />}

      {report && (
        <div className="grid grid-cols-1 gap-5 lg:grid-cols-3">
          <Panel title="Health Score">
            <div className="flex justify-center py-2">
              <HealthGauge score={report.score ?? 0} status={report.status} />
            </div>
          </Panel>

          <Panel title="Summary" className="lg:col-span-2">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              {report.summary &&
                Object.entries(report.summary).map(([k, v]) => (
                  <Stat key={k} label={humanize(k)} value={String(v)} />
                ))}
            </div>
          </Panel>

          <Panel title={`Checks (${report.checks?.length ?? 0})`} className="lg:col-span-3">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-ink-600 text-left text-[11px] uppercase tracking-wide text-fg-faint">
                    <th className="px-2 py-2">State</th>
                    <th className="px-2 py-2">Check</th>
                    <th className="px-2 py-2">Severity</th>
                    <th className="px-2 py-2 text-right">Penalty</th>
                    <th className="px-2 py-2">Detail</th>
                  </tr>
                </thead>
                <tbody>
                  {report.checks?.map((c) => (
                    <tr
                      key={c.id}
                      className="border-b border-ink-600/50 align-top hover:bg-ink-700/40"
                    >
                      <td className="px-2 py-2">
                        {c.passed ? (
                          <span className="text-sev-ok">●</span>
                        ) : (
                          <span className="text-sev-critical">●</span>
                        )}
                      </td>
                      <td className="px-2 py-2 text-fg">{c.name}</td>
                      <td className="px-2 py-2">
                        {c.passed ? (
                          <span className="text-xs text-fg-faint">—</span>
                        ) : (
                          <SeverityBadge severity={c.severity} />
                        )}
                      </td>
                      <td className="px-2 py-2 text-right text-fg-muted">
                        {c.penalty > 0 ? `−${c.penalty}` : "0"}
                      </td>
                      <td className="px-2 py-2 text-fg-muted">{c.message}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Panel>
        </div>
      )}
    </div>
  );
}

function humanize(key: string): string {
  return key
    .replace(/([A-Z])/g, " $1")
    .replace(/^./, (c) => c.toUpperCase())
    .trim();
}
