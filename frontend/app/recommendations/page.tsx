"use client";

import { useCallback, useEffect, useState } from "react";
import { useCluster } from "@/components/ClusterContext";
import { Panel, Stat } from "@/components/Panel";
import { PageHeader, RefreshButton } from "@/components/PageHeader";
import { SeverityBadge } from "@/components/SeverityBadge";
import { Loading } from "@/components/States";
import { ANALYZERS } from "@/lib/analyzers";
import { fetchAnalysis, normalizeFindings } from "@/lib/api";
import { Severity } from "@/lib/types";

interface Row {
  analyzer: string;
  type: string;
  severity: Severity;
  resource: string;
  message: string;
}

const sevRank: Record<Severity, number> = { critical: 0, warning: 1, info: 2 };

export default function Page() {
  const { clusterId, namespace } = useCluster();
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(true);
  const [unavailable, setUnavailable] = useState<string[]>([]);

  const load = useCallback(() => {
    setLoading(true);
    setRows([]);
    setUnavailable([]);
    Promise.all(
      ANALYZERS.map(async (a) => {
        const r = await fetchAnalysis(a.endpoint, clusterId, {
          namespace: a.needsNamespace ? namespace : undefined,
        });
        if (!r.data) return { label: a.label, rows: [] as Row[], failed: r.status };
        const rows = normalizeFindings(a.key, r.data).map((f) => ({
          analyzer: a.label,
          ...f,
        }));
        return { label: a.label, rows, failed: 0 };
      }),
    ).then((results) => {
      const all = results.flatMap((x) => x.rows);
      all.sort((a, b) => {
        const r = sevRank[a.severity] - sevRank[b.severity];
        if (r !== 0) return r;
        return a.analyzer.localeCompare(b.analyzer);
      });
      setRows(all);
      setUnavailable(
        results.filter((x) => x.failed && x.failed !== 200).map((x) => x.label),
      );
      setLoading(false);
    });
  }, [clusterId, namespace]);

  useEffect(() => load(), [load]);

  const counts = rows.reduce(
    (acc, r) => {
      acc[r.severity]++;
      return acc;
    },
    { critical: 0, warning: 0, info: 0 } as Record<Severity, number>,
  );

  return (
    <div>
      <PageHeader
        title="Recommendations"
        subtitle="Every finding across all analyzers, prioritized by severity."
        right={<RefreshButton onClick={load} loading={loading} />}
      />

      {loading && rows.length === 0 && <Loading what="all analyzers" />}

      {!loading && (
        <div className="space-y-5">
          <div className="grid grid-cols-3 gap-3 sm:max-w-md">
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
          </div>

          {unavailable.length > 0 && (
            <p className="text-xs text-fg-faint">
              Unavailable: {unavailable.join(", ")} (data source unreachable or not
              configured).
            </p>
          )}

          <Panel title={`Prioritized findings (${rows.length})`}>
            {rows.length === 0 ? (
              <div className="flex items-center gap-2 py-6 text-sm text-sev-ok">
                <span className="inline-block h-2 w-2 rounded-full bg-sev-ok" />
                No findings across any analyzer.
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-ink-600 text-left text-[11px] uppercase tracking-wide text-fg-faint">
                      <th className="px-2 py-2">Severity</th>
                      <th className="px-2 py-2">Analyzer</th>
                      <th className="px-2 py-2">Type</th>
                      <th className="px-2 py-2">Resource</th>
                      <th className="px-2 py-2">Message</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((r, i) => (
                      <tr
                        key={i}
                        className="border-b border-ink-600/50 align-top hover:bg-ink-700/40"
                      >
                        <td className="px-2 py-2">
                          <SeverityBadge severity={r.severity} />
                        </td>
                        <td className="px-2 py-2 text-fg-muted">{r.analyzer}</td>
                        <td className="px-2 py-2 text-fg">{r.type}</td>
                        <td className="px-2 py-2 break-all text-fg-muted">
                          {r.resource}
                        </td>
                        <td className="px-2 py-2 text-fg-muted">{r.message}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Panel>
        </div>
      )}
    </div>
  );
}
