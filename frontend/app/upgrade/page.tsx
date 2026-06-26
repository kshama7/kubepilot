"use client";

import { AnalysisView } from "@/components/AnalysisView";
import { useCluster } from "@/components/ClusterContext";

export default function Page() {
  const { target, setTarget } = useCluster();
  return (
    <div>
      <div className="mb-4 flex items-center gap-2 rounded-md border border-ink-600 bg-ink-800 px-4 py-2.5">
        <span className="text-[11px] uppercase tracking-wide text-fg-faint">
          target version
        </span>
        <input
          value={target}
          placeholder="next minor"
          onChange={(e) => setTarget(e.target.value)}
          spellCheck={false}
          className="w-40 rounded border border-ink-600 bg-ink-900 px-2 py-1 text-sm text-fg outline-none placeholder:text-fg-faint focus:border-sev-info"
        />
        <span className="text-xs text-fg-faint">
          e.g. 1.25 — empty defaults to the next minor after the cluster version
        </span>
      </div>
      <AnalysisView analyzer="upgrade" />
    </div>
  );
}
