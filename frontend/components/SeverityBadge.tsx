import { Severity } from "@/lib/types";

const styles: Record<Severity, string> = {
  critical: "text-sev-critical border-sev-critical/40 bg-sev-critical/10",
  warning: "text-sev-warning border-sev-warning/40 bg-sev-warning/10",
  info: "text-sev-info border-sev-info/40 bg-sev-info/10",
};

export function SeverityBadge({ severity }: { severity: Severity }) {
  return (
    <span
      className={`inline-block rounded border px-1.5 py-0.5 text-[11px] uppercase tracking-wide ${styles[severity]}`}
    >
      {severity}
    </span>
  );
}

export function SeverityDot({ severity }: { severity: Severity }) {
  const color: Record<Severity, string> = {
    critical: "bg-sev-critical",
    warning: "bg-sev-warning",
    info: "bg-sev-info",
  };
  return (
    <span className={`inline-block h-2 w-2 rounded-full ${color[severity]}`} />
  );
}
