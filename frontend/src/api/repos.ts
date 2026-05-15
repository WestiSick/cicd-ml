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

export async function addRepo(input: { url: string; branches?: string[] }): Promise<Repo> {
  return await api<Repo>("/api/repos", {
    method: "POST",
    body: JSON.stringify(input),
  });
}
