import { useEffect, useState } from "react";

import { StatusChip } from "./StatusChip";
import { formatDuration, formatSignedPercent } from "@/lib/format";

/* QueueCard — one card per in-flight (or just-completed) workflow_run.
 *
 * Renders:
 *   - status chip
 *   - repo / branch / SHA
 *   - predicted_sec (always when we have one)
 *   - actual_sec    (only on completed)
 *   - δ%            (only on completed when prediction available)
 *   - live elapsed timer when status=running
 *   - progress bar (predicted/elapsed ratio) when status=running
 *
 * Designed for a vertical stack on /dashboard. ~64px tall so 8 cards
 * fit above the fold on 1440×900.
 */
export type QueueCardData = {
  run_id?: number;
  job_id?: number;
  repo?: string;
  branch?: string;
  head_sha?: string;
  workflow?: string;
  job_name?: string;
  status?: string;        // "queued" | "in_progress" | "running" | "completed" | etc.
  conclusion?: string;
  predicted_sec?: number;
  actual_sec?: number;
  delta_pct?: number;
  startedAt?: string;     // ISO; client-side estimate for the live timer
};

export function QueueCard({ data, fresh }: { data: QueueCardData; fresh?: boolean }) {
  const [now, setNow] = useState(() => Date.now());

  const isRunning = data.status === "running" || data.status === "in_progress";

  // Tick once a second for the live elapsed counter — only while running.
  useEffect(() => {
    if (!isRunning) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [isRunning]);

  const startedMs = data.startedAt ? new Date(data.startedAt).getTime() : 0;
  const elapsedSec = isRunning && startedMs > 0 ? Math.max(0, (now - startedMs) / 1000) : 0;
  const pct =
    isRunning && data.predicted_sec && data.predicted_sec > 0
      ? Math.min(100, (elapsedSec / data.predicted_sec) * 100)
      : data.status === "completed"
      ? 100
      : 0;

  const status = (() => {
    if (data.status === "completed" && data.conclusion === "success") return "done";
    if (data.status === "completed") return "failed";
    if (isRunning) return "running";
    return "queued";
  })() as Parameters<typeof StatusChip>[0]["status"];

  return (
    <div
      style={{
        padding: "var(--s-3) var(--s-4)",
        background: fresh ? "var(--accent-soft)" : "var(--bg-elevated)",
        border: "1px solid var(--border-subtle)",
        borderRadius: "var(--r-8)",
        transition: "background var(--t-entry) var(--ease)",
      }}
    >
      <div style={{ display: "grid", gridTemplateColumns: "auto 1fr auto auto auto auto", gap: "var(--s-3)", alignItems: "center" }}>
        <StatusChip status={status} />
        <div>
          <div className="mono" style={{ fontSize: "var(--fs-13)" }}>
            {data.repo ?? "—"}
            {data.workflow && <span style={{ color: "var(--text-tertiary)" }}> · {data.workflow}</span>}
          </div>
          <div className="mono" style={{ fontSize: 11, color: "var(--text-tertiary)", marginTop: 2 }}>
            {data.branch ?? "—"}{data.head_sha ? " · " + data.head_sha.slice(0, 7) : ""}
            {data.job_name && " · " + data.job_name}
          </div>
        </div>
        <Metric label="predicted" value={data.predicted_sec !== undefined ? formatDuration(data.predicted_sec) : "—"} accent />
        <Metric
          label={isRunning ? "elapsed" : "actual"}
          value={
            isRunning
              ? formatDuration(elapsedSec)
              : data.actual_sec !== undefined
              ? formatDuration(data.actual_sec)
              : "—"
          }
        />
        <Metric
          label="δ"
          value={data.delta_pct !== undefined ? formatSignedPercent(data.delta_pct) : "—"}
          colour={deltaColour(data.delta_pct)}
        />
        <span className="mono caps" style={{ fontSize: 10, color: "var(--text-tertiary)" }}>
          #{data.run_id ?? data.job_id ?? "?"}
        </span>
      </div>
      {/* Progress bar — when running, shows elapsed-vs-predicted. When
          completed, always full. When queued, hidden. */}
      {(isRunning || data.status === "completed") && (
        <div
          style={{
            marginTop: 8, height: 3,
            background: "var(--bg-inset)",
            borderRadius: "var(--r-pill)", overflow: "hidden",
          }}
        >
          <div
            style={{
              height: "100%",
              width: `${pct}%`,
              background: pct > 100 ? "var(--warn)" : "var(--accent)",
              transition: "width 0.5s linear",
            }}
          />
        </div>
      )}
    </div>
  );
}

function Metric({ label, value, accent, colour }: { label: string; value: string; accent?: boolean; colour?: string }) {
  return (
    <div style={{ textAlign: "right" }}>
      <div className="caps" style={{ fontSize: 10, color: "var(--text-tertiary)" }}>{label}</div>
      <div
        className="mono"
        style={{
          fontSize: "var(--fs-13)",
          color: colour ?? (accent ? "var(--accent)" : "var(--text-primary)"),
        }}
      >
        {value}
      </div>
    </div>
  );
}

function deltaColour(d: number | undefined): string {
  if (d === undefined) return "var(--text-tertiary)";
  const abs = Math.abs(d);
  if (abs <= 10) return "var(--ok)";
  if (abs <= 30) return "var(--warn)";
  return "var(--err)";
}
