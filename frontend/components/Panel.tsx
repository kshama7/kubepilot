import { ReactNode } from "react";

export function Panel({
  title,
  right,
  children,
  className = "",
}: {
  title?: string;
  right?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section
      className={`rounded-md border border-ink-600 bg-ink-800 ${className}`}
    >
      {(title || right) && (
        <header className="flex items-center justify-between border-b border-ink-600 px-4 py-2.5">
          {title && (
            <h2 className="text-xs font-semibold uppercase tracking-wider text-fg-muted">
              {title}
            </h2>
          )}
          {right}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

export function Stat({
  label,
  value,
  accent = "text-fg",
}: {
  label: string;
  value: ReactNode;
  accent?: string;
}) {
  return (
    <div className="rounded border border-ink-600 bg-ink-700/40 px-3 py-2">
      <div className="text-[11px] uppercase tracking-wide text-fg-faint">
        {label}
      </div>
      <div className={`mt-0.5 text-lg ${accent}`}>{value}</div>
    </div>
  );
}
