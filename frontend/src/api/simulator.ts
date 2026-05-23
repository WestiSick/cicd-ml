import { api } from "./client";

export type SimMetrics = {
  strategy: string;
  jobs_count: number;
  makespan_sec: number;
  wait_p50_sec: number;
  wait_p95_sec: number;
  wait_mean_sec: number;
  throughput_per_min: number;
  sla_violations: number;
  window_start: string;
  window_end: string;
};

export type SimRunRow = {
  id: number;
  strategy: string;
  window_start: string;
  window_end: string;
  jobs_count: number;
  makespan_sec?: number;
  wait_p50_sec?: number;
  wait_p95_sec?: number;
  throughput_per_min?: number;
  sla_violations?: number;
  extra: Record<string, unknown>;
  created_at: string;
};

export type RunSimulatorRequest = {
  window_start?: string;
  window_end?: string;
  repo_ids?: number[];
  strategies?: string[];
  runners?: number;
  sla_main_sec?: number;
  sla_feature_sec?: number;
};

export type RunSimulatorResponse = {
  jobs: number;
  runners: number;
  results: SimMetrics[];
};

export async function listStrategies(): Promise<string[]> {
  const r = await api<{ strategies: string[] }>("/api/simulator/strategies");
  return r.strategies;
}

export async function runSimulator(req: RunSimulatorRequest): Promise<RunSimulatorResponse> {
  return await api<RunSimulatorResponse>("/api/simulator/run", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function listSimRuns(limit = 50): Promise<SimRunRow[]> {
  const r = await api<{ runs: SimRunRow[] }>(`/api/simulator/runs?limit=${limit}`);
  return r.runs;
}

// Browser-direct CSV download URL.
export function simRunExportCSVURL(id: number): string {
  const base = (import.meta.env.VITE_API_BASE as string) || "http://localhost:8080";
  return `${base}/api/simulator/runs/${id}/export.csv`;
}
