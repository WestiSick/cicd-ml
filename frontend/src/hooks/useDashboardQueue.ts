import { useEffect, useState } from "react";

import { fetchQueue, type QueueRow } from "@/api/queue";
import { useWebSocket } from "./useWebSocket";
import type { QueueCardData } from "@/components/QueueCard";

/* useDashboardQueue
 *
 * Maintains the dashboard's "active job" map by combining:
 *   - one-shot REST /api/queue on mount (so the page isn't empty on
 *     first load — covers reloads mid-run);
 *   - /ws/queue WebSocket events that update entries in place by run_id.
 *
 * Why a Map keyed by run_id rather than a flat list of events:
 *   - The dashboard wants ONE card per run, updating in place as the
 *     run progresses (requested → in_progress → completed). Appending to
 *     a list duplicates cards every time GitHub fires an event.
 *   - On `completed` we keep the card in the map for 30s so the user
 *     sees the final δ-error before it drops out.
 */
export function useDashboardQueue(): QueueCardData[] {
  const [byRunID, setByRunID] = useState<Map<number, QueueCardData & { _completedAt?: number }>>(new Map());

  // Seed from REST.
  useEffect(() => {
    let cancelled = false;
    fetchQueue(50)
      .then((rows) => {
        if (cancelled) return;
        const next = new Map<number, QueueCardData>();
        for (const r of rows) {
          // /api/queue is keyed by job_id, not run_id. We use job_id as
          // the dedup key in this case — a workflow_run with many jobs
          // will show each one separately, which matches user mental
          // model better than collapsing.
          next.set(r.job_id, queueRowToCard(r));
        }
        setByRunID(next);
      })
      .catch(() => { /* silent — WS will catch up */ });
    return () => { cancelled = true; };
  }, []);

  // Live updates from /ws/queue.
  useWebSocket("/ws/queue", (msg) => {
    if (!msg.type.startsWith("workflow_run.")) return;
    const data = (msg.data ?? {}) as Record<string, unknown>;
    const runID = typeof data.run_id === "number" ? data.run_id : undefined;
    if (runID === undefined) return;

    setByRunID((cur) => {
      const next = new Map(cur);
      const prev = next.get(runID) ?? {};
      const merged: QueueCardData & { _completedAt?: number } = {
        ...prev,
        run_id: runID,
        repo:          typeof data.repo === "string" ? data.repo : prev.repo,
        branch:        typeof data.branch === "string" ? data.branch : prev.branch,
        workflow:      typeof data.workflow === "string" ? data.workflow : prev.workflow,
        head_sha:      typeof data.head_sha === "string" ? data.head_sha : prev.head_sha,
        status:        typeof data.status === "string" ? data.status : prev.status,
        conclusion:    typeof data.conclusion === "string" ? data.conclusion : prev.conclusion,
        predicted_sec:      typeof data.predicted_sec      === "number" ? data.predicted_sec      : prev.predicted_sec,
        predicted_raw_sec:  typeof data.predicted_raw_sec  === "number" ? data.predicted_raw_sec  : prev.predicted_raw_sec,
        calibration_factor: typeof data.calibration_factor === "number" ? data.calibration_factor : prev.calibration_factor,
        actual_sec:    typeof data.actual_sec    === "number" ? data.actual_sec    : prev.actual_sec,
        delta_pct:     typeof data.delta_pct     === "number" ? data.delta_pct     : prev.delta_pct,
      };
      // Stamp the moment we transition into in_progress so the live
      // elapsed timer in QueueCard has a base. We don't trust the
      // payload's run_started_at because clock skew between GitHub and
      // the browser blows up the elapsed display.
      const wentRunning = merged.status === "in_progress" && prev.status !== "in_progress";
      if (wentRunning && !merged.startedAt) {
        merged.startedAt = new Date().toISOString();
      }
      if (merged.status === "completed" && !merged._completedAt) {
        merged._completedAt = Date.now();
      }
      next.set(runID, merged);
      return next;
    });
  });

  // Sweep completed entries after 30s so the list doesn't grow forever.
  useEffect(() => {
    const id = window.setInterval(() => {
      setByRunID((cur) => {
        const cutoff = Date.now() - 30_000;
        let mutated = false;
        const next = new Map(cur);
        for (const [k, v] of cur) {
          if (v._completedAt && v._completedAt < cutoff) {
            next.delete(k);
            mutated = true;
          }
        }
        return mutated ? next : cur;
      });
    }, 5_000);
    return () => window.clearInterval(id);
  }, []);

  // Sort: running first, then queued, then completed (newest at top
  // within each band).
  return Array.from(byRunID.values()).sort((a, b) => {
    return statusOrder(a.status) - statusOrder(b.status);
  });
}

function statusOrder(s?: string): number {
  if (s === "in_progress" || s === "running") return 0;
  if (s === "queued" || s === "requested" || s === "waiting") return 1;
  if (s === "completed") return 2;
  return 3;
}

function queueRowToCard(r: QueueRow): QueueCardData {
  return {
    job_id:        r.job_id,
    repo:          r.repo,
    workflow:      r.workflow,
    job_name:      r.job_name,
    branch:        r.head_branch,
    head_sha:      r.head_sha,
    status:        r.status,
    conclusion:    r.conclusion,
    predicted_sec: r.predicted_sec,
    actual_sec:    r.actual_sec,
  };
}
