// A minimal semicircular health-score gauge. Color tracks the same thresholds
// the backend uses: healthy ≥ 90, degraded ≥ 70, else critical.
export function HealthGauge({
  score,
  status,
}: {
  score: number;
  status?: string;
}) {
  const clamped = Math.max(0, Math.min(100, score));
  const color =
    clamped >= 90
      ? "var(--ok)"
      : clamped >= 70
        ? "var(--warn)"
        : "var(--crit)";

  // Semicircle: 180 degrees, radius 80, centered.
  const r = 80;
  const cx = 100;
  const cy = 100;
  const circumference = Math.PI * r;
  const offset = circumference * (1 - clamped / 100);

  return (
    <div
      className="flex flex-col items-center"
      style={
        {
          ["--ok" as string]: "#3ecf8e",
          ["--warn" as string]: "#e6a23c",
          ["--crit" as string]: "#f0506e",
        } as React.CSSProperties
      }
    >
      <svg viewBox="0 0 200 120" className="w-56">
        <path
          d={`M ${cx - r} ${cy} A ${r} ${r} 0 0 1 ${cx + r} ${cy}`}
          fill="none"
          stroke="#1e2632"
          strokeWidth="14"
          strokeLinecap="round"
        />
        <path
          d={`M ${cx - r} ${cy} A ${r} ${r} 0 0 1 ${cx + r} ${cy}`}
          fill="none"
          stroke={color}
          strokeWidth="14"
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={offset}
        />
        <text
          x={cx}
          y={cy - 8}
          textAnchor="middle"
          className="fill-fg"
          style={{ fontSize: 30, fontFamily: "ui-monospace, monospace" }}
        >
          {clamped}
        </text>
        <text
          x={cx}
          y={cy + 12}
          textAnchor="middle"
          style={{ fontSize: 11, fill: "#566173", letterSpacing: 1 }}
        >
          / 100
        </text>
      </svg>
      {status && (
        <div
          className="mt-1 text-sm uppercase tracking-wide"
          style={{ color }}
        >
          {status}
        </div>
      )}
    </div>
  );
}
