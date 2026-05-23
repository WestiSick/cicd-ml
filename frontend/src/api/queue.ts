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
