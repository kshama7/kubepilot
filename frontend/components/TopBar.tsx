"use client";

import { useCluster } from "./ClusterContext";

export function TopBar() {
  const { clusterId, namespace, setClusterId, setNamespace } = useCluster();
  return (
    <header className="flex items-center gap-4 border-b border-ink-600 bg-ink-800/60 px-6 py-2.5">
      <Field label="cluster">
        <input
          value={clusterId}
          onChange={(e) => setClusterId(e.target.value)}
          spellCheck={false}
          className="w-40 rounded border border-ink-600 bg-ink-900 px-2 py-1 text-sm text-fg outline-none focus:border-sev-info"
        />
      </Field>
      <Field label="namespace">
        <input
          value={namespace}
          placeholder="all"
          onChange={(e) => setNamespace(e.target.value)}
          spellCheck={false}
          className="w-40 rounded border border-ink-600 bg-ink-900 px-2 py-1 text-sm text-fg outline-none placeholder:text-fg-faint focus:border-sev-info"
        />
      </Field>
      <div className="ml-auto text-xs text-fg-faint">
        Kubernetes Reliability Platform
      </div>
    </header>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="flex items-center gap-2">
      <span className="text-[11px] uppercase tracking-wide text-fg-faint">
        {label}
      </span>
      {children}
    </label>
  );
}
