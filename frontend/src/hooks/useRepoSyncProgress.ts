import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { listBGJobs, type BGJob } from "@/api/bgjobs";
import { useWebSocket } from "./useWebSocket";

/* useRepoSyncProgress
 *
 * Maps repo-slug → live sync stats parsed from the most recent
 * collect_history bg_job's `logs_tail`. Backed by:
 *   1. one-shot REST load on mount (so we have data before the first WS
 *      message arrives — covers reload-mid-sync);
 *   2. /ws/bg-jobs subscription that overlays new stats as bg_jobs
 *      updates fire.
 *
 * Returns a plain object keyed by `owner/name` because that's how
 * collect_history payloads identify their target. Repo cards look up
 * their own slug; absent → no progress chip.
 *
 * Why hook-level rather than per-card: the WS connection is a singleton
 * (see useWebSocket), and parsing JSON on every event costs nothing.
 * Keeping the map in one place avoids each card re-subscribing.
 */
export type RepoSyncStats = {
  phase?: "fetching_meta" | "fetching_runs" | "rate_limited" | "done" | string;
  page?: number;
  runs_seen?: number;
  runs_total?: number;
  jobs_per_sec?: number;
  eta_seconds?: number;
  rate_remaining?: number;
  rate_limit?: number;
  rate_reset_unix?: number;
  // Surfaced from the bg_job row itself, not the JSON blob — useful
  // for the chip colour (running vs failed vs done).
  status?: BGJob["status"];
  // Pretty message from bg_job — shown as a one-liner under the bar.
  message?: string;
};

export function useRepoSyncProgress(): Record<string, RepoSyncStats> {
  const [byRepo, setByRepo] = useState<Record<string, RepoSyncStats>>({});
  const qc = useQueryClient();

  // Seed from REST. Newest-first ordering means the first match per repo
  // is the live one.
  useEffect(() => {
    let cancelled = false;
    listBGJobs({ limit: 100 })
      .then((jobs) => {
        if (cancelled) return;
        const seen: Record<string, RepoSyncStats> = {};
        for (const j of jobs) {
          if (j.kind !== "collect_history") continue;
          const slug = repoOf(j);
          if (!slug || slug in seen) continue;
          const stats = parseStats(j);
          if (stats) seen[slug] = stats;
        }
        setByRepo(seen);
      })
      .catch(() => {
        // Silent — bad network on first paint is fine; the WS will catch up.
      });
    return () => { cancelled = true; };
  }, []);

  // Live updates from /ws/bg-jobs. Every bg_job update broadcasts the
  // full row, so we can read logs_tail straight off the payload.
  useWebSocket("/ws/bg-jobs", (msg) => {
    if (msg.type !== "bg_job.updated") return;
    const job = msg.data as BGJob | undefined;
    if (!job || job.kind !== "collect_history") return;
    const slug = repoOf(job);
    if (!slug) return;
    const stats = parseStats(job);
    if (!stats) return;
    setByRepo((cur) => ({ ...cur, [slug]: stats }));
    // Also invalidate the repos query so the card re-renders with fresh
    // counters (runs_count/jobs_count come from the repos table).
    qc.invalidateQueries({ queryKey: ["repos"] });
  });

  return byRepo;
}

function repoOf(job: BGJob): string | null {
  const p = job.payload as Record<string, unknown> | null;
  if (!p) return null;
  const r = p.repo;
  return typeof r === "string" ? r : null;
}

function parseStats(job: BGJob): RepoSyncStats | null {
  // The backend writes a JSON blob in logs_tail (see syncStats in
  // services/api-gateway/internal/github/sync.go). For older rows that
  // predate the structured payload, logs_tail is empty — we still return
  // a minimal stats object so the card shows the bg_job status.
  let parsed: Partial<RepoSyncStats> = {};
  if (job.logs_tail) {
    try {
      parsed = JSON.parse(job.logs_tail);
    } catch {
      // Non-JSON tail — keep as-is, just expose the bg_job state.
    }
  }
  return {
    ...parsed,
    status: job.status,
    message: job.message,
  };
}
