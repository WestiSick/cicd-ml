import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { listBGJobs, type BGJob } from "@/api/bgjobs";
import { useWebSocket } from "@/hooks/useWebSocket";
import { StatusChip } from "@/components/StatusChip";
import { LanguageSwitcher, useT } from "@/i18n";

const PHASE_LABEL: Record<BGJob["kind"], string> = {
  bootstrap:         "01  Setup chain",
  collect_history:   "02  Data collection",
  compute_features:  "03  Feature extraction",
  train_model:       "04  Model training",
  simulate:          "05  Strategy simulation",
  refresh:           "    Refresh",
};

/* Live progress for the bootstrap chain.
 *
 * Combines:
 *   1. Initial REST load of recent bg_jobs (so reloads work mid-flight).
 *   2. WebSocket subscription to /ws/bg-jobs for live updates.
 *
 * State is a Map keyed by job id so updates are O(1). Sorting happens at
 * render time — cheap because the list is small (a few dozen jobs max).
 */
export function SetupProgress({ bootstrapId }: { bootstrapId: number }) {
  const t = useT();
  const initial = useQuery({
    queryKey: ["bg-jobs-initial", bootstrapId],
    queryFn: () => listBGJobs({ limit: 200 }),
  });

  const [jobs, setJobs] = useState<Map<number, BGJob>>(new Map());

  // Seed from REST.
  useEffect(() => {
    if (initial.data) {
      setJobs(new Map(initial.data.map((j) => [j.id, j])));
    }
  }, [initial.data]);

  // Apply WebSocket updates.
  useWebSocket("/ws/bg-jobs", (msg) => {
    if (msg.type === "bg_job.updated" && msg.data) {
      const job = msg.data as BGJob;
      setJobs((cur) => {
        const next = new Map(cur);
        next.set(job.id, job);
        return next;
      });
    }
  });

  const grouped = useMemo(() => {
    const all = Array.from(jobs.values()).filter(
      (j) =>
        j.kind === "bootstrap" ||
        j.kind === "collect_history" ||
        j.kind === "compute_features" ||
        j.kind === "train_model" ||
        j.kind === "simulate"
    );
    const byKind: Record<string, BGJob[]> = {};
    for (const j of all) {
      (byKind[j.kind] ||= []).push(j);
    }
    for (const k of Object.keys(byKind)) {
      byKind[k].sort((a, b) => a.id - b.id);
    }
    return byKind;
  }, [jobs]);

  const phaseOrder: BGJob["kind"][] = [
    "bootstrap",
    "collect_history",
    "compute_features",
    "train_model",
    "simulate",
  ];

  return (
    <div style={{ position: "relative" }}>
      <div style={{ position: "absolute", top: "var(--s-4)", right: "var(--s-6)", zIndex: 1 }}>
        <LanguageSwitcher />
      </div>
      <div style={{ maxWidth: 760, margin: "0 auto", padding: "var(--s-12) var(--s-6) var(--s-16)" }}>
      <div className="caps" style={{ color: "var(--accent)", marginBottom: "var(--s-2)" }}>
        {t("setup.label")} — running
      </div>
      <h1
        style={{
          margin: 0,
          fontSize: "var(--fs-28)",
          fontWeight: 500,
          letterSpacing: "-0.01em",
        }}
      >
        {t("setup.progress.title")}
      </h1>
      <p
        style={{
          margin: "var(--s-2) 0 var(--s-8)",
          color: "var(--text-secondary)",
          fontSize: "var(--fs-14)",
        }}
      >
        {t("setup.progress.intro")}
      </p>

      {phaseOrder.map((phase) => {
        const list = grouped[phase] ?? [];
        if (list.length === 0) return null;
        return (
          <div key={phase} style={{ marginBottom: "var(--s-6)" }}>
            <div
              className="mono caps"
              style={{
                color: "var(--text-tertiary)",
                marginBottom: "var(--s-2)",
                fontSize: 11,
              }}
            >
              {PHASE_LABEL[phase]}
            </div>
            <div style={{ display: "grid", gap: "var(--s-2)" }}>
              {list.map((j) => (
                <JobRow key={j.id} job={j} />
              ))}
            </div>
          </div>
        );
      })}
      </div>
    </div>
  );
}

function JobRow({ job }: { job: BGJob }) {
  const pct = job.total > 0 ? Math.round((job.progress / job.total) * 100) : 0;
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "120px 1fr 80px",
        alignItems: "center",
        gap: "var(--s-3)",
        padding: "var(--s-2) var(--s-3)",
        border: "1px solid var(--border-subtle)",
        borderRadius: "var(--r-6)",
        background: "var(--bg-elevated)",
      }}
    >
      <div style={{ display: "flex", gap: "var(--s-2)", alignItems: "center" }}>
        <StatusChip status={job.status} />
      </div>
      <div>
        <div style={{ fontSize: "var(--fs-13)" }}>
          {job.message || labelForPayload(job)}
        </div>
        <div
          style={{
            marginTop: 6,
            height: 3,
            background: "var(--bg-inset)",
            borderRadius: "var(--r-pill)",
            overflow: "hidden",
          }}
        >
          <div
            style={{
              height: "100%",
              width: `${pct}%`,
              background: job.status === "failed" ? "var(--err)" : "var(--accent)",
              transition: "width var(--t-entry) var(--ease)",
            }}
          />
        </div>
      </div>
      <div
        className="mono"
        style={{
          textAlign: "right",
          fontSize: "var(--fs-12)",
          color: "var(--text-tertiary)",
        }}
      >
        {job.progress}/{job.total || "—"}
      </div>
    </div>
  );
}

function labelForPayload(job: BGJob): string {
  const p = job.payload as Record<string, unknown>;
  if (typeof p?.repo === "string") return p.repo;
  if (typeof p?.algo === "string") return `algo: ${p.algo}`;
  return job.kind;
}
