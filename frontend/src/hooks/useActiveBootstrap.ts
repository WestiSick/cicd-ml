import { useQuery } from "@tanstack/react-query";

import { listBGJobs, type BGJob } from "@/api/bgjobs";

/* Detects whether a bootstrap bg_job is in flight server-side.
 *
 * Necessary because the /setup page's local state is lost on page
 * reload. Without this hook, a user who refreshes after clicking
 * "Start setup" lands back on the empty form even though bootstrap
 * is still chugging through phase 1.
 *
 * The hook looks at the most recent bootstrap bg_job and returns:
 *   - the row itself if it's queued / running / done (FinishOnDone
 *     hasn't flipped bootstrap_done yet — show progress so the user
 *     sees the last phase finish).
 *   - null when the latest bootstrap is failed/cancelled, so the user
 *     can retry from the form.
 *   - null when there's never been a bootstrap — pure first-run case.
 *
 * Polls every 3s while we're waiting; once we have an in-flight job
 * the SetupProgress component takes over polling per-id.
 */
export function useActiveBootstrap(): {
  job: BGJob | null;
  isLoading: boolean;
} {
  const q = useQuery({
    queryKey: ["active-bootstrap"],
    queryFn: () => listBGJobs({ limit: 20 }),
    refetchInterval: 3_000,
  });

  const job =
    (q.data ?? [])
      // Newest-first ordering is the API's default, but be defensive.
      .slice()
      .sort((a, b) => b.id - a.id)
      .find(
        (j) =>
          j.kind === "bootstrap" &&
          j.status !== "failed" &&
          j.status !== "cancelled",
      ) ?? null;

  return { job, isLoading: q.isLoading };
}
