import { api } from "./client";

export type BGJob = {
  id: number;
  kind: "bootstrap" | "collect_history" | "compute_features" | "train_model" | "simulate" | "refresh";
  payload: Record<string, unknown>;
  status: "queued" | "running" | "done" | "failed" | "cancelled";
  progress: number;
  total: number;
  message?: string;
  logs_tail?: string;
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};

export async function listBGJobs(opts: { status?: string; limit?: number } = {}): Promise<BGJob[]> {
  const params = new URLSearchParams();
  if (opts.status) params.set("status", opts.status);
  if (opts.limit) params.set("limit", String(opts.limit));
  const qs = params.toString();
  const r = await api<{ jobs: BGJob[] }>(`/api/bg-jobs${qs ? "?" + qs : ""}`);
  return r.jobs;
}

export async function getBGJob(id: number): Promise<BGJob> {
  return await api<BGJob>(`/api/bg-jobs/${id}`);
}
