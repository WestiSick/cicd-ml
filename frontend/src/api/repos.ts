import { api } from "./client";

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
};

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
