import { api } from "./client";

export type CustomWeights = {
  short_job: number;
  deadline_proximity: number;
  branch_importance: number;
};

export type ActiveModelSummary = {
  id: number;
  name: string;
  algo: string;
  metrics?: Record<string, number>;
};

export type SystemState = {
  bootstrap_done: boolean;
  active_model?: ActiveModelSummary;
  active_strategy?: string;
  custom_weights: CustomWeights;
};

export type AdminSettingsBody = {
  active_strategy?: string;
  custom_weights?: CustomWeights;
  github_token?: string | null; // empty string or null clears
};

export async function saveAdminSettings(body: AdminSettingsBody): Promise<SystemState> {
  return api<SystemState>("/api/admin/settings", { method: "POST", body: JSON.stringify(body) });
}

/* GET /api/system/state — gates whether the user sees /setup or the app.
 *
 * Returns a permissive default if the API is not reachable yet (e.g. the
 * compose stack is still booting). The bootstrap gate in App.tsx treats
 * "unknown" as "show setup" rather than crashing.
 */
export async function fetchSystemState(): Promise<SystemState> {
  try {
    return await api<SystemState>("/api/system/state");
  } catch {
    return {
      bootstrap_done: false,
      custom_weights: { short_job: 1, deadline_proximity: 0.5, branch_importance: 0.3 },
    };
  }
}
