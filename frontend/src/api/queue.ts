import { api } from "./client";

/* /api/queue — snapshot of in-flight + just-completed jobs.
 *
 * One row per `jobs` record in `queued/in_progress/requested/waiting`
 * status, plus jobs completed in the last 5 minutes (the latter so a
 * just-finished card stays visible long enough for the user to see the
 * δ-error before it drops out). Joined with the most recent prediction
 * for the job.
 *
 * The /dashboard page combines this REST snapshot (initial render) with
 * /ws/queue events (live updates) — see useDashboardQueue. */
export type QueueRow = {
  job_id: number;
  repo: string;
  workflow: string;
  job_name: string;
  head_branch?: string;
  head_sha?: string;
  status: string;
  conclusion?: string;
  predicted_sec?: number;
  actual_sec?: number;
};

export async function fetchQueue(limit = 50): Promise<QueueRow[]> {
  const r = await api<{ queue: QueueRow[] }>(`/api/queue?limit=${limit}`);
  return r.queue;
}

/* /api/queue/history — persistent log of predicted vs actual at
 * workflow_run.completed time. Populated by the webhook handler so
 * every reload of /history shows the same data — unlike /dashboard's
 * live feed which is in-memory and dies on browser reload.
 *
 * The row carries the calibration math captured at the moment the
 * user saw the prediction: raw model output, factor applied, and the
 * final number on the dashboard. That lets the history page show
 * "model said X, calibration adjusted to Y, reality was Z, off by W%"
 * with full traceability. */
export type HistoryRow = {
  id: number;
  run_id: number;
  repo: string;
  workflow?: string;
  head_branch?: string;
  head_sha?: string;
  event?: string;
  conclusion?: string;
  predicted_sec?: number;
  predicted_raw_sec?: number;
  calibration_factor?: number;
  actual_sec?: number;
  delta_pct?: number;
  model_id?: number;
  model_algo?: string;
  completed_at: string;
};

export type HistoryFilters = {
  limit?: number;          // 1..500, default 100
  repo?: string;           // exact "owner/name"
  hours?: number;          // 0 = all, default 168 (7d), max 720 (30d)
  minAbsDelta?: number;    // |delta_pct| floor for "show me the misses"
};

export async function fetchQueueHistory(f: HistoryFilters = {}): Promise<HistoryRow[]> {
  const qs = new URLSearchParams();
  if (f.limit !== undefined) qs.set("limit", String(f.limit));
  if (f.repo)                qs.set("repo", f.repo);
  if (f.hours !== undefined) qs.set("hours", String(f.hours));
  if (f.minAbsDelta !== undefined && f.minAbsDelta > 0) qs.set("min_abs_delta", String(f.minAbsDelta));
  const tail = qs.toString();
  const r = await api<{ rows: HistoryRow[] }>(`/api/queue/history${tail ? "?" + tail : ""}`);
  return r.rows || [];
}
