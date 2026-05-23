import { api } from "./client";

export type WebhookStatus =
  | "not_attempted"
  | "installed"
  | "failed_no_access"
  | "failed_unreachable"
  | "failed_other";

export type Repo = {
  id: number;
  owner: string;
  name: string;
  github_id?: number;
  default_branch?: string;
  tracked_branches: string[];
  status: "idle" | "fetching" | "synced" | "error" | "paused";
  last_synced_at?: string;
  oldest_run_at?: string;
  newest_run_at?: string;
  runs_count: number;
  jobs_count: number;
  last_error?: string;
  is_seed: boolean;
  added_at: string;

  // Webhook auto-install tracking — populated by the api-gateway after a
  // best-effort POST /repos/{owner}/{repo}/hooks call on GitHub. UI shows
  // a status badge on the repo card with this.
  webhook_id?: number;
  webhook_url?: string;
  webhook_installed_at?: string;
  webhook_status: WebhookStatus;
  webhook_error?: string;
};

export type InstallWebhookResponse = {
  status: WebhookStatus;
  hook_id?: number;
  callback?: string;
  error?: string;
};

export async function installRepoWebhook(id: number): Promise<InstallWebhookResponse> {
  return api<InstallWebhookResponse>(`/api/repos/${id}/webhook`, { method: "POST" });
}

export async function removeRepoWebhook(id: number): Promise<{ removed: boolean; warning?: string }> {
  return api(`/api/repos/${id}/webhook`, { method: "DELETE" });
}

export async function listRepos(): Promise<Repo[]> {
  const r = await api<{ repos: Repo[] }>("/api/repos");
  return r.repos;
}

export type AddRepoInput = {
  url: string;
  branches?: string[];
  history_months?: 3 | 6 | 12;
  github_token?: string;
  /** When omitted (default), the backend auto-enqueues a collect_history
   *  bg_job so the new repo starts fetching immediately. Pass `false` to
   *  defer that and only sync explicitly. */
  auto_sync?: boolean;
};

export async function addRepo(input: AddRepoInput): Promise<Repo> {
  return await api<Repo>("/api/repos", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export type SyncRepoResponse = {
  bg_job_id: number;
  repo_id: number;
  message: string;
};

export async function syncRepo(
  id: number,
  opts: { history_months?: 3 | 6 | 12; github_token?: string } = {},
): Promise<SyncRepoResponse> {
  return await api<SyncRepoResponse>(`/api/repos/${id}/sync`, {
    method: "POST",
    body: JSON.stringify(opts),
  });
}

export async function pauseRepo(id: number): Promise<void> {
  await api(`/api/repos/${id}/pause`, { method: "POST" });
}

export async function resumeRepo(id: number): Promise<void> {
  await api(`/api/repos/${id}/resume`, { method: "POST" });
}

export async function resyncRepo(id: number): Promise<SyncRepoResponse> {
  return await api<SyncRepoResponse>(`/api/repos/${id}/resync`, { method: "POST" });
}

export async function deleteRepo(id: number): Promise<{ deleted: boolean; rows_deleted: number }> {
  return await api(`/api/repos/${id}`, { method: "DELETE" });
}

// /api/datasets — high-level totals shown in the dataset summary card.
export type DatasetSummary = {
  repo_count: number;
  run_count: number;
  job_count: number;
  features_count: number;
};

export async function fetchDatasetsSummary(): Promise<DatasetSummary> {
  return api<DatasetSummary>("/api/datasets");
}

// /api/datasets/{id} — per-repo stats for the detail page.
export type DatasetDetail = {
  repo: Repo;
  duration_buckets: Array<{ label: string; lo: number; hi: number; count: number }>;
  top_workflows:    Array<{ name: string; runs: number; p50_sec: number; p95_sec: number }>;
  top_jobs:         Array<{ name: string; runs: number; mean_sec: number; p50_sec: number }>;
  branch_breakdown: Array<{ branch: string; runs: number; mean_sec: number }>;
  conclusion_counts: Record<string, number>;
};

export async function fetchDatasetDetail(id: number): Promise<DatasetDetail> {
  return api<DatasetDetail>(`/api/datasets/${id}`);
}

// /api/datasets/coverage — for the heatmap.
export type CoverageResponse = {
  days: string[];                                          // YYYY-MM-DD spine
  repos: Array<{ id: number; slug: string }>;
  cells: Array<{ repo_id: number; day: string; count: number }>;
};

export async function fetchDatasetsCoverage(days = 90): Promise<CoverageResponse> {
  return api<CoverageResponse>(`/api/datasets/coverage?days=${days}`);
}

// /api/datasets/timeline — daily run counts for the wizard's cutoff bar.
export type TimelineResponse = {
  days: number;
  cells: Array<{ day: string; count: number }>;
};

export async function fetchTimeline(opts: { days?: number; repoIDs?: number[] } = {}): Promise<TimelineResponse> {
  const qs = new URLSearchParams();
  if (opts.days) qs.set("days", String(opts.days));
  if (opts.repoIDs && opts.repoIDs.length > 0) qs.set("repo_ids", opts.repoIDs.join(","));
  const tail = qs.toString();
  return api<TimelineResponse>(`/api/datasets/timeline${tail ? "?" + tail : ""}`);
}

// /api/datasets/{id}/push-recommendations — when-to-push heatmap.
//
// Cells are sparse: only (hour, dow) buckets that actually have data
// are returned, so the frontend has to build the dense 24×7 grid
// itself. Deltas are signed percent vs the repo's overall mean —
// negative is good (faster than average).
export type PushRecCell = {
  hour: number;                 // 0..23
  dow: number;                  // 0..6, Mon=0
  sample_count: number;
  mean_wait_sec: number;
  mean_duration_sec: number;
  mean_total_sec: number;
  wait_delta_pct: number;
  duration_delta_pct: number;
  total_delta_pct: number;
};

export type PushRecommendations = {
  repo_id: number;
  days: number;
  tz: string;
  window_start: string;
  window_end: string;
  overall: {
    sample_count: number;
    mean_wait_sec: number;
    mean_duration_sec: number;
    mean_total_sec: number;
  };
  cells: PushRecCell[];
  best:  { hour: number; dow: number; total_delta_pct: number } | null;
  worst: { hour: number; dow: number; total_delta_pct: number } | null;
};

export async function fetchPushRecommendations(
  id: number,
  opts: { days?: number; tz?: string } = {},
): Promise<PushRecommendations> {
  const qs = new URLSearchParams();
  if (opts.days) qs.set("days", String(opts.days));
  if (opts.tz)   qs.set("tz",   opts.tz);
  const tail = qs.toString();
  return api(`/api/datasets/${id}/push-recommendations${tail ? "?" + tail : ""}`);
}

// /api/datasets/{id}/features — for the feature-matrix preview panel.
export type FeaturePreviewRow = {
  job_id: number;
  job_name: string;
  duration_sec?: number;
  head_branch?: string;
  head_sha?: string;
  created_at: string;
  features: Record<string, number | string | null>;
};

// Browser-direct URL for the dataset CSV export. We don't fetch the
// bytes through api() because the response is a binary stream — let the
// browser handle the download flow.
export function datasetExportCSVURL(id: number): string {
  const base = (import.meta.env.VITE_API_BASE as string) || "http://localhost:8080";
  return `${base}/api/datasets/${id}/export.csv`;
}

export async function fetchFeaturePreview(
  id: number,
  opts: { limit?: number; jobName?: string } = {},
): Promise<{ rows: FeaturePreviewRow[]; limit: number }> {
  const qs = new URLSearchParams();
  if (opts.limit) qs.set("limit", String(opts.limit));
  if (opts.jobName) qs.set("job_name", opts.jobName);
  const tail = qs.toString();
  return api(`/api/datasets/${id}/features${tail ? "?" + tail : ""}`);
}
