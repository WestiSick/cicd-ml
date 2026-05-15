import { api } from "./client";

export type SetupRequest = {
  github_token?: string;
  repos: string[];          // owner/name slugs
  history_months: 3 | 6 | 12;
  models: string[];         // algo ids
};

export type SetupResponse = {
  bg_job_id: number;
  message: string;
};

export async function startSetup(req: SetupRequest): Promise<SetupResponse> {
  return await api<SetupResponse>("/api/setup/start", {
    method: "POST",
    body: JSON.stringify(req),
  });
}
