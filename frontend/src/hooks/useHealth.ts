import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

type State = "ok" | "degraded" | "down";

/* Combines /healthz (cheap liveness) with the system state into a single
 * traffic-light. Down → red, any 5xx during polling → degraded, all-good
 * → green. */
export function useHealth(): { state: State; label: string } {
  const q = useQuery({
    queryKey: ["healthz"],
    queryFn: () => api<{ status: string; time: string }>("/healthz"),
    refetchInterval: 10_000,
    retry: 0,
  });

  if (q.isError) {
    return { state: "down", label: "API unreachable" };
  }
  if (q.isLoading) {
    return { state: "degraded", label: "Checking..." };
  }
  return { state: "ok", label: "All systems operational" };
}
