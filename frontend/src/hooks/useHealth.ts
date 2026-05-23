import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

type State = "ok" | "degraded" | "down";

type HealthResponse = {
  state: "ok" | "degraded" | "down";
  components: Array<{ name: string; state: State; message?: string }>;
  time: string;
};

/* Per-component traffic light shown in the top bar. Pulls the same
 * /api/admin/health endpoint /admin → System health renders, so the
 * shapка-точка and the /admin breakdown can never disagree.
 *
 * Earlier this hook called `/healthz` (the trivial liveness probe on
 * the api binary itself). That works in dev but the prod Traefik
 * routes only `/api/*`, `/ws/*`, and `/webhooks/*` to the gateway —
 * `/healthz` without the `/api/` prefix lands on the nginx frontend
 * and returns 404, which made the top-bar dot show DOWN even when
 * every backend service was green. */
export function useHealth(): { state: State; label: string } {
  const q = useQuery<HealthResponse>({
    queryKey: ["health-top-bar"],
    queryFn: () => api<HealthResponse>("/api/admin/health"),
    refetchInterval: 10_000,
    retry: 0,
  });

  if (q.isError) {
    return { state: "down", label: "API unreachable" };
  }
  if (q.isLoading || !q.data) {
    return { state: "degraded", label: "Checking..." };
  }
  const labelByState: Record<State, string> = {
    ok:       "All systems operational",
    degraded: "Some services degraded — see /admin → System health",
    down:     "One or more services are down — see /admin → System health",
  };
  return { state: q.data.state, label: labelByState[q.data.state] };
}
