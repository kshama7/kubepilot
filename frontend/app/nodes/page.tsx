"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { useCluster } from "@/components/ClusterContext";
import { Panel, Stat } from "@/components/Panel";
import { PageHeader, RefreshButton } from "@/components/PageHeader";
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
  const detail = (id: string, key: string): string[] => {
    const check = report?.checks?.find((c) => c.id === id);
    const v = check?.details?.[key];
    return Array.isArray(v) ? (v as string[]) : [];
  };

  const notReady = detail("node-readiness", "notReadyNodes");
  const pressured = detail("resource-pressure", "pressuredNodes");
  const cordoned = detail("node-schedulability", "cordonedNodes");
  const summary = report?.summary as
    | { totalNodes?: number; readyNodes?: number; pressureNodes?: number; cordonedNodes?: number }
    | undefined;

  return (
    <div>
      <PageHeader
        title="Nodes"
        subtitle="Node readiness, resource pressure, and schedulability from the cluster-health analyzer."
        right={<RefreshButton onClick={load} loading={loading} />}
      />

      {loading && !report && <Loading what="node health" />}
      {result?.error && <ErrorState status={result.status} error={result.error} />}

      {report && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <Stat label="Total" value={summary?.totalNodes ?? 0} />
            <Stat
              label="Ready"
              value={summary?.readyNodes ?? 0}
              accent="text-sev-ok"
            />
            <Stat
              label="Under Pressure"
              value={summary?.pressureNodes ?? 0}
              accent={summary?.pressureNodes ? "text-sev-warning" : "text-fg-muted"}
            />
            <Stat
              label="Cordoned"
              value={summary?.cordonedNodes ?? 0}
              accent={summary?.cordonedNodes ? "text-sev-warning" : "text-fg-muted"}
            />
          </div>

          <div className="grid grid-cols-1 gap-5 lg:grid-cols-3">
            <NodeList title="NotReady" nodes={notReady} tone="critical" />
            <NodeList title="Resource Pressure" nodes={pressured} tone="warning" />
            <NodeList title="Cordoned" nodes={cordoned} tone="warning" />
          </div>

          <p className="text-xs text-fg-faint">
            For per-node utilization trends and saturation prediction, see{" "}
            <Link href="/capacity" className="text-sev-info hover:underline">
              Capacity
            </Link>
            .
          </p>
        </div>
      )}
    </div>
  );
}

function NodeList({
  title,
  nodes,
  tone,
}: {
  title: string;
  nodes: string[];
  tone: "critical" | "warning";
}) {
  const color = tone === "critical" ? "text-sev-critical" : "text-sev-warning";
  return (
    <Panel title={title}>
      {nodes.length === 0 ? (
        <div className="text-sm text-sev-ok">none</div>
      ) : (
        <ul className="space-y-1 text-sm">
          {nodes.map((n) => (
            <li key={n} className={color}>
              {n}
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
