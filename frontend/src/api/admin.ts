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
