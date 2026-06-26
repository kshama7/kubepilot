import { ReactNode } from "react";

export function PageHeader({
  title,
  subtitle,
  right,
}: {
  title: string;
  subtitle?: string;
  right?: ReactNode;
}) {
  return (
    <div className="mb-5 flex items-end justify-between">
      <div>
        <h1 className="text-xl font-semibold tracking-tight text-fg">{title}</h1>
        {subtitle && (
          <p className="mt-1 text-sm text-fg-muted">{subtitle}</p>
        )}
      </div>
      {right}
    </div>
  );
}

export function RefreshButton({
  onClick,
  loading,
}: {
  onClick: () => void;
  loading: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={loading}
      className="rounded border border-ink-600 bg-ink-700 px-3 py-1.5 text-xs text-fg-muted hover:border-sev-info hover:text-fg disabled:opacity-50"
    >
      {loading ? "running…" : "↻ re-run"}
    </button>
  );
}
