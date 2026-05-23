import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { EmptyState } from "@/components/EmptyState";
import { Button } from "@/components/Button";
import { StatusChip } from "@/components/StatusChip";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useT } from "@/i18n";
import { fetchSystemState } from "@/api/system";
import { formatDuration, formatSignedPercent } from "@/lib/format";
import { QueueCard } from "@/components/QueueCard";
import { SparklineChart } from "@/components/SparklineChart";
import { useDashboardQueue } from "@/hooks/useDashboardQueue";
import { fetchLoad24h } from "@/api/dashboard";

type QueueEvent = {
  type: string;
  repo?: string;
  branch?: string;
  run_id?: number;
  workflow?: string;
  status?: string;
  conclusion?: string;
  head_sha?: string;
  predicted_sec?: number; // populated by api-gateway via ml-service on workflow_run.requested
  actual_sec?: number;    // populated on workflow_run.completed (workflow-level updated_at - run_started_at)
  delta_pct?: number;     // signed Δ% (predicted - actual) / actual · 100 — only on completed with a remembered prediction
  model_algo?: string;
  receivedAt: string;     // client-side
};

/* Dashboard — KPIs + live feed of webhook arrivals.
 *
 * The live feed is a thin client-side projection of /ws/queue messages.
 * The backend already publishes "workflow_run.requested|in_progress|completed"
 * when GitHub posts to /webhooks/github (see internal/http/webhook.go).
 *
 * We hold only the last 20 events in memory — anything older than that is
 * visible in /datasets (per-repo history) anyway. Keeps the page snappy
 * during heavy push windows. */
export function Dashboard() {
  const t = useT();
  const [events, setEvents] = useState<QueueEvent[]>([]);

  // System state — gives us the active model id/algo/MAE and the active
  // strategy. Polled at 5s; refresh isn't critical, the values only change
  // when the user clicks Activate.
  const systemQ = useQuery({
    queryKey: ["system-state"],
    queryFn: fetchSystemState,
    refetchInterval: 5_000,
  });

  // Active-job cards, keyed by run_id. Combines REST seed with WS
  // updates — see useDashboardQueue for the merging logic.
  const activeCards = useDashboardQueue();

  // 24h load — sparkline next to the KPI. Polled at 60s; the chart's
  // bucket granularity is 1h so faster refresh adds nothing visible.
  const load24hQ = useQuery({
    queryKey: ["load-24h"],
    queryFn: fetchLoad24h,
    refetchInterval: 60_000,
  });

  // KPI: mean wait — average of (started_at - run created) for the most
  // recent N completed runs. We derive from activeCards (which carries
  // both predicted and actual); a more rigorous version would query the
  // /api/queue endpoint with a wider lookback.
  const meanWait = (() => {
    const completed = activeCards.filter((c) => c.actual_sec !== undefined);
    if (completed.length === 0) return undefined;
    const sum = completed.reduce((acc, c) => acc + (c.actual_sec ?? 0), 0);
    return sum / completed.length;
  })();

  const { connected } = useWebSocket("/ws/queue", (msg) => {
    if (!msg.type.startsWith("workflow_run.")) return;
    const data = (msg.data ?? {}) as Record<string, unknown>;
    const ev: QueueEvent = {
      type: msg.type,
      repo:          typeof data.repo === "string" ? data.repo : undefined,
      branch:        typeof data.branch === "string" ? data.branch : undefined,
      run_id:        typeof data.run_id === "number" ? data.run_id : undefined,
      workflow:      typeof data.workflow === "string" ? data.workflow : undefined,
      status:        typeof data.status === "string" ? data.status : undefined,
      conclusion:    typeof data.conclusion === "string" ? data.conclusion : undefined,
      head_sha:      typeof data.head_sha === "string" ? data.head_sha : undefined,
      predicted_sec: typeof data.predicted_sec === "number" ? data.predicted_sec : undefined,
      actual_sec:    typeof data.actual_sec    === "number" ? data.actual_sec    : undefined,
      delta_pct:     typeof data.delta_pct     === "number" ? data.delta_pct     : undefined,
      model_algo:    typeof data.model_algo    === "string" ? data.model_algo    : undefined,
      receivedAt: new Date().toISOString(),
    };
    setEvents((cur) => [ev, ...cur].slice(0, 20));
  });

  return (
    <>
      <PageHeader
        title={t("dashboard.title")}
        subtitle={t("dashboard.subtitle")}
        actions={<Button variant="secondary" disabled>{t("common.pause_queue")}</Button>}
      />

      <section
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(4, 1fr)",
          gap: "var(--s-3)",
          marginBottom: "var(--s-6)",
        }}
      >
        <Kpi
          label={t("dashboard.kpi.active_model")}
          value={systemQ.data?.active_model ? `${systemQ.data.active_model.algo}` : "—"}
          hint={
            systemQ.data?.active_model?.metrics?.test_mae !== undefined
              ? `MAE ${formatDuration(systemQ.data.active_model.metrics.test_mae)}`
              : t("dashboard.kpi.not_trained")
          }
        />
        <Kpi
          label={t("dashboard.kpi.strategy")}
          value={systemQ.data?.active_strategy ? systemQ.data.active_strategy.toUpperCase() : "—"}
          hint={systemQ.data?.active_strategy ? "/admin → strategy" : t("dashboard.kpi.not_configured")}
        />
        <Kpi
          label={t("dashboard.kpi.live_feed")}
          value={connected ? t("common.online") : t("common.offline")}
          hint={connected ? t("dashboard.kpi.ws_connected") : t("dashboard.kpi.reconnecting")}
        />
        <Card>
          <div className="caps" style={{ color: "var(--text-tertiary)" }}>{t("dashboard.kpi.mean_wait")}</div>
          <div
            className="mono"
            style={{
              marginTop: "var(--s-2)",
              fontSize: "var(--fs-28)",
              fontWeight: 500,
              letterSpacing: "-0.01em",
            }}
          >
            {meanWait !== undefined ? formatDuration(meanWait) : "—"}
          </div>
          <div style={{ marginTop: "var(--s-1)" }}>
            <SparklineChart
              points={(load24hQ.data ?? []).map((b) => ({ label: b.hour.slice(11, 16) + " UTC", value: b.jobs }))}
            />
          </div>
          <div style={{ marginTop: 4, color: "var(--text-tertiary)", fontSize: "var(--fs-12)" }}>
            {t("dashboard.kpi.load24h.hint", { n: (load24hQ.data ?? []).reduce((acc, b) => acc + b.jobs, 0) })}
          </div>
        </Card>
      </section>

      <h2 style={sectionTitleStyle}>
        {t("dashboard.queue")}
        <span style={{ marginLeft: 8, color: "var(--text-tertiary)", fontSize: "var(--fs-12)", fontWeight: 400 }}>
          {activeCards.length > 0 ? `(${activeCards.length})` : ""}
        </span>
      </h2>
      {activeCards.length === 0 ? (
        <EmptyState
          title={t("dashboard.queue.empty.title")}
          hint={t("dashboard.queue.empty.hint")}
        />
      ) : (
        <div style={{ display: "grid", gap: "var(--s-2)", marginBottom: "var(--s-6)" }}>
          {activeCards.map((c, i) => (
            <QueueCard key={c.run_id ?? c.job_id ?? i} data={c} fresh={i < 1 && c.status === "in_progress"} />
          ))}
        </div>
      )}

      <h2 style={sectionTitleStyle}>{t("dashboard.live_feed")}</h2>
      {events.length === 0 ? (
        <EmptyState
          title={t("dashboard.empty.title")}
          hint={connected ? t("dashboard.empty.hint_connected") : t("dashboard.empty.hint_disconnected")}
        />
      ) : (
        <Card>
          <div style={{ display: "grid", gap: 0 }}>
            {events.map((e, i) => (
              <EventRow key={`${e.run_id}-${e.type}-${e.receivedAt}`} event={e} fresh={i < 2} />
            ))}
          </div>
        </Card>
      )}
    </>
  );
}

function Kpi({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Card>
      <div className="caps" style={{ color: "var(--text-tertiary)" }}>{label}</div>
      <div
        className="mono"
        style={{
          marginTop: "var(--s-2)",
          fontSize: "var(--fs-28)",
          fontWeight: 500,
          letterSpacing: "-0.01em",
        }}
      >
        {value}
      </div>
      {hint && (
        <div style={{ marginTop: "var(--s-1)", color: "var(--text-tertiary)", fontSize: "var(--fs-12)" }}>
          {hint}
        </div>
      )}
    </Card>
  );
}

function EventRow({ event, fresh }: { event: QueueEvent; fresh: boolean }) {
  const action = event.type.replace("workflow_run.", "");
  const status = (() => {
    if (action === "completed" && event.conclusion === "success") return "done";
    if (action === "completed") return "failed";
    if (action === "in_progress") return "running";
    return "queued";
  })() as Parameters<typeof StatusChip>[0]["status"];

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "70px 90px 1fr 1fr 80px 80px 80px 70px",
        alignItems: "center",
        gap: "var(--s-2)",
        padding: "8px 0",
        borderTop: "1px solid var(--border-subtle)",
        background: fresh ? "var(--accent-soft)" : "transparent",
        transition: "background 4s linear",
      }}
    >
      <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-tertiary)" }}>
        {new Date(event.receivedAt).toISOString().slice(11, 19)}
      </span>
      <StatusChip status={status} />
      <span className="mono" style={{ fontSize: "var(--fs-13)" }}>{event.repo ?? "—"}</span>
      <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-secondary)" }}>
        {event.branch ? `${event.branch} · ${(event.head_sha ?? "").slice(0, 7)}` : "—"}
      </span>
      {/* Predicted column — same accent colour as before so the live
          forecasting is the visual highlight even when actual is missing. */}
      <span
        className="mono"
        style={{
          fontSize: "var(--fs-12)",
          color: event.predicted_sec !== undefined ? "var(--accent)" : "var(--text-tertiary)",
          textAlign: "right",
        }}
        title={event.model_algo ? `predicted by ${event.model_algo}` : "no prediction"}
      >
        {event.predicted_sec !== undefined ? formatDuration(event.predicted_sec) : "—"}
      </span>
      {/* Actual column — populated only on workflow_run.completed. */}
      <span
        className="mono"
        style={{
          fontSize: "var(--fs-12)",
          color: event.actual_sec !== undefined ? "var(--text-primary)" : "var(--text-tertiary)",
          textAlign: "right",
        }}
      >
        {event.actual_sec !== undefined ? formatDuration(event.actual_sec) : "—"}
      </span>
      {/* Δ-error column — coloured by accuracy band: green (≤10%), warn
          (10–30%), err (>30%). Gives an at-a-glance reading of model
          quality during live demo. */}
      <span
        className="mono"
        style={{
          fontSize: "var(--fs-12)",
          color: deltaColor(event.delta_pct),
          textAlign: "right",
        }}
        title="signed prediction error: (predicted − actual) / actual"
      >
        {event.delta_pct !== undefined ? formatSignedPercent(event.delta_pct) : "—"}
      </span>
      <span className="mono caps" style={{ fontSize: 11, color: "var(--text-tertiary)", textAlign: "right" }}>
        {action}
      </span>
    </div>
  );
}

// deltaColor maps prediction error to a meaningful palette band:
//   - ≤10%  → ok green: "model nailed it"
//   - ≤30%  → warn yellow: "usable but not great"
//   - >30%  → err red: "poor — investigate this run"
//
// Sign doesn't affect colour; only magnitude matters. The user can see
// the sign from formatSignedPercent's +/− prefix.
function deltaColor(delta: number | undefined): string {
  if (delta === undefined) return "var(--text-tertiary)";
  const abs = Math.abs(delta);
  if (abs <= 10) return "var(--ok)";
  if (abs <= 30) return "var(--warn)";
  return "var(--err)";
}

const sectionTitleStyle: React.CSSProperties = {
  fontSize: "var(--fs-16)",
  fontWeight: 500,
  margin: "0 0 var(--s-3)",
};
