// Shared loading / error / unavailable states with the tooling aesthetic.

export function Loading({ what = "analysis" }: { what?: string }) {
  return (
    <div className="flex items-center gap-2 px-1 py-6 text-sm text-fg-muted">
      <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-sev-info" />
      Running {what}…
    </div>
  );
}

// Distinguishes "feature not configured" (503) from a real transport error.
export function ErrorState({ status, error }: { status: number; error: string }) {
  const unavailable = status === 503;
  const color = unavailable ? "text-sev-warning" : "text-sev-critical";
  const label = unavailable ? "Not configured" : `Error (${status || "no response"})`;
  return (
    <div className={`rounded border border-ink-600 bg-ink-700/40 px-4 py-4 text-sm`}>
      <div className={`mb-1 uppercase tracking-wide ${color}`}>{label}</div>
      <div className="text-fg-muted">{error}</div>
      {status === 0 && (
        <div className="mt-2 text-xs text-fg-faint">
          The dashboard proxies to the API at KUBEPILOT_API_URL — is the backend
          running?
        </div>
      )}
    </div>
  );
}
