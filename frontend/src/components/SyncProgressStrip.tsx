import { useEffect, useState } from "react";

import type { RepoSyncStats } from "@/hooks/useRepoSyncProgress";
import { useT } from "@/i18n";
import { formatDuration } from "@/lib/format";

/* SyncProgressStrip — the thin live-strip under the /datasets card.
 *
 * Shows (left → right): progress bar, percent, jobs/sec, ETA, rate
 * counter with countdown to reset. Re-renders on a 1s tick so the
 * countdown is smooth without backend pushes.
 *
 * Layout deliberately compact: takes one row, ~36px tall. The strip
 * appears only while sync is actively running — done/idle states hide
 * it so the card returns to its quiet shape.
 */
export function SyncProgressStrip({ stats }: { stats: RepoSyncStats | undefined }) {
  const t = useT();
  const [now, setNow] = useState(() => Date.now());

  // 1Hz refresh for the rate-limit countdown. Cheap — only the
  // affected strip rerenders.
  useEffect(() => {
    if (!stats) return;
    if (stats.phase === "done" || stats.status === "done" || stats.status === "failed" || stats.status === "cancelled") {
      return;
    }
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [stats]);

  if (!stats) return null;
  // Don't render the strip when sync is finished — the regular
  // counters on the card already show the post-sync state.
  if (stats.status === "done" || stats.status === "failed" || stats.status === "cancelled" || stats.phase === "done") {
    return null;
  }

  const seen = stats.runs_seen ?? 0;
  const total = stats.runs_total ?? 0;
  const pct = total > 0 ? Math.min(100, Math.round((seen / total) * 100)) : 0;
  const rateLeft = stats.rate_reset_unix ? Math.max(0, stats.rate_reset_unix * 1000 - now) : 0;

  return (
    <div style={{ marginTop: "var(--s-3)", paddingTop: "var(--s-2)", borderTop: "1px dashed var(--border-subtle)" }}>
      {/* Bar */}
      <div style={{ height: 3, background: "var(--bg-inset)", borderRadius: "var(--r-pill)", overflow: "hidden" }}>
        <div
          style={{
            height: "100%",
            width: `${pct}%`,
            background: stats.phase === "rate_limited" ? "var(--warn)" : "var(--accent)",
            transition: "width var(--t-entry) var(--ease)",
          }}
        />
      </div>

      {/* Stats row — monospace so columns line up across cards */}
      <div
        className="mono"
        style={{
          marginTop: 6,
          display: "flex",
          flexWrap: "wrap",
          gap: "var(--s-3)",
          fontSize: 11,
          color: "var(--text-tertiary)",
        }}
      >
        {total > 0 && (
          <span>
            {seen.toLocaleString()} / {total.toLocaleString()} ({pct}%)
          </span>
        )}
        {stats.page !== undefined && stats.page > 0 && (
          <span>{t("datasets.sync.page", { n: stats.page })}</span>
        )}
        {stats.jobs_per_sec !== undefined && stats.jobs_per_sec > 0 && (
          <span>{stats.jobs_per_sec.toFixed(1)} jobs/s</span>
        )}
        {stats.eta_seconds !== undefined && stats.eta_seconds > 0 && (
          <span>ETA {formatDuration(stats.eta_seconds)}</span>
        )}
        {stats.rate_limit !== undefined && stats.rate_limit > 0 && (
          <span style={{ color: rateLow(stats.rate_remaining, stats.rate_limit) ? "var(--warn)" : undefined }}>
            {t("datasets.sync.rate", {
              remaining: stats.rate_remaining ?? 0,
              limit: stats.rate_limit,
            })}
            {rateLeft > 0 && stats.phase === "rate_limited" && (
              <> · reset in {formatDuration(rateLeft / 1000)}</>
            )}
          </span>
        )}
      </div>

      {stats.message && (
        <div
          className="mono"
          style={{
            marginTop: 4,
            fontSize: 11,
            color: "var(--text-secondary)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
          title={stats.message}
        >
          {stats.message}
        </div>
      )}
    </div>
  );
}

// rateLow = "under 10% of limit remaining"; flag yellow so the user
// notices before we hit the hard cap.
function rateLow(remaining: number | undefined, limit: number): boolean {
  if (remaining === undefined) return false;
  return remaining < Math.max(50, limit * 0.1);
}
