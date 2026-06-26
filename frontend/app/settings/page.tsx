"use client";

import { useState } from "react";
import { useCluster } from "@/components/ClusterContext";
import { Panel } from "@/components/Panel";
import { PageHeader } from "@/components/PageHeader";
import { apiGet } from "@/lib/api";

export default function Page() {
  const { clusterId, namespace, target, setClusterId, setNamespace, setTarget } =
    useCluster();
  const [aiStatus, setAiStatus] = useState<string>("unknown");
  const [aiChecking, setAiChecking] = useState(false);

  const checkAI = () => {
    setAiChecking(true);
    setAiStatus("checking…");
    // /explain returns 503 when the key is unset, 200/4xx otherwise.
    apiGet(
      `/api/v1/clusters/${encodeURIComponent(clusterId)}/explain?analyzer=cluster_health`,
    ).then((r) => {
      if (r.status === 503) setAiStatus("disabled (no API key on backend)");
      else if (r.status === 0) setAiStatus("backend unreachable");
      else setAiStatus("enabled");
      setAiChecking(false);
    });
  };

  return (
    <div>
      <PageHeader
        title="Settings"
        subtitle="Dashboard context and backend connectivity. Context is persisted in your browser."
      />

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <Panel title="Analysis Context">
          <Row label="Cluster ID">
            <input
              value={clusterId}
              onChange={(e) => setClusterId(e.target.value)}
              spellCheck={false}
              className="input"
            />
          </Row>
          <Row label="Namespace">
            <input
              value={namespace}
              placeholder="all"
              onChange={(e) => setNamespace(e.target.value)}
              spellCheck={false}
              className="input"
            />
          </Row>
          <Row label="Upgrade Target">
            <input
              value={target}
              placeholder="next minor"
              onChange={(e) => setTarget(e.target.value)}
              spellCheck={false}
              className="input"
            />
          </Row>
        </Panel>

        <Panel title="Backend">
          <p className="text-sm text-fg-muted">
            The dashboard proxies <code className="text-fg">/api/*</code> to the Go
            API via Next.js rewrites — set{" "}
            <code className="text-fg">KUBEPILOT_API_URL</code> (default{" "}
            <code className="text-fg">http://localhost:8080</code>) before starting
            the dashboard.
          </p>

          <div className="mt-4 flex items-center gap-3">
            <button
              onClick={checkAI}
              disabled={aiChecking}
              className="rounded border border-ink-600 bg-ink-700 px-3 py-1.5 text-xs text-fg-muted hover:border-sev-info hover:text-fg disabled:opacity-50"
            >
              test AI explanation layer
            </button>
            <span className="text-sm text-fg-muted">{aiStatus}</span>
          </div>
          <p className="mt-2 text-xs text-fg-faint">
            Enable AI by setting KUBEPILOT_ANTHROPIC_API_KEY on the backend. The AI
            layer only explains deterministic findings — it never generates them.
          </p>
        </Panel>
      </div>

      <style jsx>{`
        :global(.input) {
          width: 16rem;
          border-radius: 0.25rem;
          border: 1px solid #1e2632;
          background: #0a0e14;
          padding: 0.375rem 0.5rem;
          font-size: 0.875rem;
          color: #c9d4e3;
          outline: none;
        }
        :global(.input:focus) {
          border-color: #4aa8ff;
        }
      `}</style>
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-3 flex items-center justify-between gap-4">
      <span className="text-sm text-fg-muted">{label}</span>
      {children}
    </div>
  );
}
