import { api } from "./client";

export type WebhookEvent = {
  id: number;
  received_at: string;
  delivery_id?: string;
  event_type?: string;
  repo?: string;
  hmac_valid?: boolean;
  payload: Record<string, unknown>;
  error?: string;
};

export type HealthComponent = {
  name: string;
  state: "ok" | "degraded" | "down";
  message?: string;
};

export type SystemHealth = {
  state: "ok" | "degraded" | "down";
  components: HealthComponent[];
  time: string;
};

export async function listWebhookEvents(limit = 50): Promise<WebhookEvent[]> {
  const r = await api<{ events: WebhookEvent[] }>(`/api/admin/webhooks?limit=${limit}`);
  return r.events;
}

export async function fetchSystemHealth(): Promise<SystemHealth> {
  return await api<SystemHealth>("/api/admin/health");
}

export type ActivityEntry = {
  id: number;
  // Backend column is `at` (timestamp default now()) — store/activity.go
  // serialises it under the same name. The earlier `occurred_at` here
  // never matched, so every row's time showed "—".
  at: string;
  actor?: string;
  action: string;
  target?: string;
  message?: string;
  success: boolean;
  details?: Record<string, unknown>;
};

export async function fetchActivity(limit = 100): Promise<ActivityEntry[]> {
  const r = await api<{ entries: ActivityEntry[] }>(`/api/activity?limit=${limit}`);
  return r.entries;
}

export async function pauseBGRunner(): Promise<{ paused: boolean }> {
  return api("/api/admin/bg-jobs/pause", { method: "POST" });
}
export async function resumeBGRunner(): Promise<{ paused: boolean }> {
  return api("/api/admin/bg-jobs/resume", { method: "POST" });
}

/* Per-(repo, workflow) calibration coefficients.
 *
 * factor:  multiplier applied to raw model prediction (1.0 = no bias).
 * n_observations: count of completed runs that shaped this factor.
 * last_*:  most recent observation for diagnostics. */
export type CalibrationRow = {
  owner: string;
  name: string;
  repo_id: number;
  workflow_name: string;
  factor: number;
  n_observations: number;
  last_actual_sec?: number;
  last_predicted_sec?: number;
  last_ratio?: number;
  updated_at: string;
};

export async function listCalibrations(): Promise<CalibrationRow[]> {
  const r = await api<{ calibrations: CalibrationRow[] }>("/api/admin/calibrations");
  return r.calibrations || [];
}
