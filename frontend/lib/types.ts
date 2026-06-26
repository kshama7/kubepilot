// Shared types mirroring the KubePilot backend JSON. Reports differ per analyzer,
// so report bodies are loosely typed and normalized into NormalizedFinding for
// the table — the same reduction the backend's AI layer performs server-side.

export type Severity = "critical" | "warning" | "info";

export interface NormalizedFinding {
  type: string;
  severity: Severity;
  resource: string;
  message: string;
}

// A generic finding as returned by the findings-based analyzers. Field names for
// the resource vary by analyzer; normalizeFindings handles each shape.
export interface RawFinding {
  type: string;
  severity: Severity;
  message: string;
  namespace?: string;
  pod?: string;
  container?: string;
  workload?: string;
  kind?: string;
  application?: string;
  node?: string;
  apiVersion?: string;
}

export interface AnalysisReport {
  clusterId?: string;
  generatedAt?: string;
  summary?: Record<string, unknown>;
  findings?: RawFinding[];
  // cluster-health specific
  score?: number;
  status?: string;
  checks?: HealthCheck[];
}

export interface HealthCheck {
  id: string;
  name: string;
  passed: boolean;
  severity: Severity;
  message: string;
  weight: number;
  penalty: number;
  details?: Record<string, unknown>;
}

export interface ApiError {
  error: string;
}
