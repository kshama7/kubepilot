# KubePilot Dashboard

Next.js 14 + TypeScript + Tailwind frontend for the KubePilot API. Dark,
terminal-adjacent, monospace — internal infra tooling, not a SaaS page.

## Run

```bash
npm install
KUBEPILOT_API_URL=http://localhost:8080 npm run dev   # http://localhost:3000
```

The dashboard proxies `/api/*` to the Go API via Next.js rewrites, so the browser
makes only same-origin requests — no CORS configuration on the backend.

## Pages

- **Overview** — cluster health gauge + per-analyzer finding counts.
- **Recommendations** — every finding across all analyzers, prioritized.
- **Clusters / Nodes** — cluster-health detail and node status.
- **Workloads · Resources · Reliability · GitOps · Upgrade Advisor · Security ·
  Capacity** — one page per analyzer, with severity rollups, a findings table,
  and an "Explain with AI" panel (calls the `/explain` endpoint).
- **Namespaces / Settings** — namespace scope and dashboard context (persisted in
  the browser).

## Design notes

- Cluster id, namespace, and upgrade target live in a React context persisted to
  `localStorage` and editable from the top bar.
- Reports are normalized into a single finding shape (`type · severity · resource
  · message`) — the same reduction the backend's AI layer performs.
- Endpoints that return `503` (AI not configured, no kubeconfig) render as
  "not configured" rather than errors.
