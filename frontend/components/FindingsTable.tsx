import { NormalizedFinding } from "@/lib/types";
import { SeverityBadge } from "./SeverityBadge";

export function FindingsTable({ findings }: { findings: NormalizedFinding[] }) {
  if (findings.length === 0) {
    return (
      <div className="flex items-center gap-2 px-1 py-6 text-sm text-sev-ok">
        <span className="inline-block h-2 w-2 rounded-full bg-sev-ok" />
        No findings — clean for this analyzer.
      </div>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-ink-600 text-left text-[11px] uppercase tracking-wide text-fg-faint">
            <th className="w-24 px-2 py-2 font-medium">Severity</th>
            <th className="w-56 px-2 py-2 font-medium">Type</th>
            <th className="w-72 px-2 py-2 font-medium">Resource</th>
            <th className="px-2 py-2 font-medium">Message</th>
          </tr>
        </thead>
        <tbody>
          {findings.map((f, i) => (
            <tr
              key={`${f.type}-${f.resource}-${i}`}
              className="border-b border-ink-600/50 align-top hover:bg-ink-700/40"
            >
              <td className="px-2 py-2">
                <SeverityBadge severity={f.severity} />
              </td>
              <td className="px-2 py-2 text-fg">{f.type}</td>
              <td className="px-2 py-2 text-fg-muted break-all">{f.resource}</td>
              <td className="px-2 py-2 text-fg-muted">{f.message}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
