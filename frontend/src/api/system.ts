import { api } from "./client";

export type SystemState = {
  bootstrap_done: boolean;
  active_model?: { id: number; name: string; algo: string };
  active_strategy?: string;
};

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
    return { bootstrap_done: false };
  }
}
