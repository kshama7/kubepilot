"use client";

import { useState } from "react";
import { AnalyzerKey, analyzerByKey } from "@/lib/analyzers";
import { apiGet } from "@/lib/api";
import { useCluster } from "./ClusterContext";
import { Panel } from "./Panel";

interface ExplainResp {
  model?: string;
  findingsExplained?: number;
  explanation?: string;
}

export function ExplainPanel({ analyzer }: { analyzer: AnalyzerKey }) {
  const meta = analyzerByKey(analyzer);
  const { clusterId, namespace } = useCluster();
  const [loading, setLoading] = useState(false);
  const [resp, setResp] = useState<ExplainResp | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<number>(0);

  const run = () => {
    setLoading(true);
    setError(null);
    const params = new URLSearchParams({ analyzer });
    if (meta.needsNamespace && namespace) params.set("namespace", namespace);
    apiGet<ExplainResp>(
      `/api/v1/clusters/${encodeURIComponent(clusterId)}/explain?${params}`,
    ).then((r) => {
      setStatus(r.status);
      if (r.error) setError(r.error);
      else setResp(r.data ?? null);
      setLoading(false);
    });
  };

  return (
    <Panel
      title="AI Explanation"
      right={
        <button
          onClick={run}
          disabled={loading}
          className="rounded border border-ink-600 bg-ink-700 px-3 py-1 text-xs text-fg-muted hover:border-sev-info hover:text-fg disabled:opacity-50"
        >
          {loading ? "explaining…" : "✦ explain findings"}
        </button>
      }
    >
      {!resp && !error && !loading && (
        <p className="text-sm text-fg-faint">
          Claude explains and prioritizes the deterministic findings above — it
          never generates findings of its own. Requires KUBEPILOT_ANTHROPIC_API_KEY
          on the backend.
        </p>
      )}
      {error && (
        <p className="text-sm text-sev-warning">
          {status === 503 ? "AI explanation is not configured on the backend." : error}
        </p>
      )}
      {resp && (
        <div>
          {resp.model && (
            <div className="mb-2 text-[11px] uppercase tracking-wide text-fg-faint">
              {resp.model} · {resp.findingsExplained ?? 0} findings
            </div>
          )}
          <pre className="whitespace-pre-wrap break-words font-mono text-sm leading-relaxed text-fg-muted">
            {resp.explanation}
          </pre>
        </div>
      )}
    </Panel>
  );
}
