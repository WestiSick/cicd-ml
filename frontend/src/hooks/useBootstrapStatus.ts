import { useQuery } from "@tanstack/react-query";

import { fetchSystemState } from "@/api/system";

/* Polls system state on a slow cadence (5s) so the bootstrap gate flips
 * automatically once the backend finishes setup, without the user
 * refreshing the page. */
export function useBootstrapStatus() {
  return useQuery({
    queryKey: ["system-state"],
    queryFn: fetchSystemState,
    refetchInterval: 5_000,
    staleTime: 0,
  });
}
