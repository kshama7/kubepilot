"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { useCluster } from "@/components/ClusterContext";
import { HealthGauge } from "@/components/HealthGauge";
import { Panel } from "@/components/Panel";
import { PageHeader, RefreshButton } from "@/components/PageHeader";
import { ErrorState, Loading } from "@/components/States";
import { ANALYZERS, AnalyzerMeta } from "@/lib/analyzers";
import {
  FetchResult,
  countBySeverity,
  fetchAnalysis,
  normalizeFindings,
} from "@/lib/api";
import { AnalysisReport, Severity } from "@/lib/types";

export default function Page() {
  const { clusterId } = useCluster();
  const [health, setHealth] = useState<FetchResult<AnalysisReport> | null>(null);
  const [loading, setLoading] = useState(true);
  const [nonce, setNonce] = useState(0);

  const load = useCallback(() => {
    setLoading(true);
    fetchAnalysis("health", clusterId).then((r) => {
      setHealth(r);
      setLoading(false);
    });
    setNonce((n) => n + 1);
  }, [clusterId]);

  useEffect(() => load(), [load]);

  const report = health?.data;

  return (
    <div>
      <PageHeader
        title="Overview"
        subtitle={`Reliability posture for cluster "${clusterId}".`}
        right={<RefreshButton onClick={load} loading={loading} />}
      />

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-3">
        <Panel title="Cluster Health">
          {loading && !report && <Loading what="health" />}
          {health?.error && (
            <ErrorState status={health.status} error={health.error} />
          )}
          {report && (
            <div className="flex justify-center py-2">
              <HealthGauge score={report.score ?? 0} status={report.status} />
            </div>
          )}
        </Panel>

        <Panel title="Analyzers" className="lg:col-span-2">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {ANALYZERS.filter((a) => a.key !== "cluster_health").map((a) => (
              <AnalyzerCard key={a.key} meta={a} nonce={nonce} />
            ))}
          </div>
        </Panel>
      </div>
    </div>
  );
}

function AnalyzerCard({ meta, nonce }: { meta: AnalyzerMeta; nonce: number }) {
  const { clusterId, namespace } = useCluster();
  const [counts, setCounts] = useState<Record<Severity, number> | null>(null);
  const [err, setErr] = useState<{ status: number } | null>(null);

  useEffect(() => {
    let active = true;
    fetchAnalysis(meta.endpoint, clusterId, {
      namespace: meta.needsNamespace ? namespace : undefined,
    }).then((r) => {
      if (!active) return;
      if (r.data) setCounts(countBySeverity(normalizeFindings(meta.key, r.data)));
      else setErr({ status: r.status });
    });
    return () => {
      active = false;
    };
  }, [meta, clusterId, namespace, nonce]);

  const total = counts ? counts.critical + counts.warning + counts.info : 0;

  return (
    <Link
      href={meta.href}
      className="block rounded border border-ink-600 bg-ink-700/30 px-3 py-3 hover:border-sev-info/50 hover:bg-ink-700/60"
    >
      <div className="flex items-center justify-between">
        <span className="flex items-center gap-2 text-sm text-fg">
          <span className="text-fg-faint">{meta.glyph}</span>
          {meta.label}
        </span>
        {err ? (
          <span className="text-[11px] text-fg-faint">
            {err.status === 503 ? "n/a" : `err ${err.status || "?"}`}
          </span>
        ) : counts ? (
          total === 0 ? (
            <span className="text-xs text-sev-ok">clean</span>
          ) : (
            <span className="flex items-center gap-2 text-xs">
              {counts.critical > 0 && (
                <span className="text-sev-critical">{counts.critical}C</span>
              )}
              {counts.warning > 0 && (
                <span className="text-sev-warning">{counts.warning}W</span>
              )}
              {counts.info > 0 && (
                <span className="text-sev-info">{counts.info}I</span>
              )}
            </span>
          )
        ) : (
          <span className="text-[11px] text-fg-faint">…</span>
        )}
      </div>
      <p className="mt-1 text-xs text-fg-faint">{meta.question}</p>
    </Link>
  );
}
