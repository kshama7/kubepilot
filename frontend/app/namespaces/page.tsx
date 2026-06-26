"use client";

import Link from "next/link";
import { useCluster } from "@/components/ClusterContext";
import { Panel } from "@/components/Panel";
import { PageHeader } from "@/components/PageHeader";

const PRESETS = ["", "default", "kube-system", "argocd"];

export default function Page() {
  const { namespace, setNamespace } = useCluster();

  return (
    <div>
      <PageHeader
        title="Namespaces"
        subtitle="Scope the namespaced analyzers (Workloads, Resources, Reliability, GitOps, Security) to a single namespace."
      />

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <Panel title="Active Namespace">
          <label className="mb-2 block text-[11px] uppercase tracking-wide text-fg-faint">
            namespace (empty = all namespaces)
          </label>
          <input
            value={namespace}
            placeholder="all"
            onChange={(e) => setNamespace(e.target.value)}
            spellCheck={false}
            className="w-full rounded border border-ink-600 bg-ink-900 px-3 py-2 text-sm text-fg outline-none placeholder:text-fg-faint focus:border-sev-info"
          />
          <div className="mt-3 flex flex-wrap gap-2">
            {PRESETS.map((p) => (
              <button
                key={p || "all"}
                onClick={() => setNamespace(p)}
                className={`rounded border px-2.5 py-1 text-xs ${
                  namespace === p
                    ? "border-sev-info bg-sev-info/10 text-fg"
                    : "border-ink-600 bg-ink-700 text-fg-muted hover:text-fg"
                }`}
              >
                {p || "all"}
              </button>
            ))}
          </div>
        </Panel>

        <Panel title="Scope applies to">
          <ul className="space-y-1.5 text-sm">
            {[
              ["Workloads", "/workloads"],
              ["Resources", "/resources"],
              ["Reliability", "/reliability"],
              ["GitOps", "/gitops"],
              ["Security", "/security"],
            ].map(([label, href]) => (
              <li key={href}>
                <Link href={href} className="text-sev-info hover:underline">
                  {label}
                </Link>
              </li>
            ))}
          </ul>
          <p className="mt-3 text-xs text-fg-faint">
            Cluster Health, Upgrade Advisor, and Capacity are cluster-wide and ignore
            the namespace filter.
          </p>
        </Panel>
      </div>
    </div>
  );
}
