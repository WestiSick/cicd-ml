import { useState } from "react";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { EmptyState } from "@/components/EmptyState";
import { Button } from "@/components/Button";
import { StatusChip } from "@/components/StatusChip";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useT } from "@/i18n";

type QueueEvent = {
  type: string;
  repo?: string;
  branch?: string;
  run_id?: number;
  workflow?: string;
  status?: string;
  conclusion?: string;
  head_sha?: string;
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

  const { connected } = useWebSocket("/ws/queue", (msg) => {
    if (!msg.type.startsWith("workflow_run.")) return;
    const data = (msg.data ?? {}) as Record<string, unknown>;
    const ev: QueueEvent = {
      type: msg.type,
      repo:       typeof data.repo === "string" ? data.repo : undefined,
      branch:     typeof data.branch === "string" ? data.branch : undefined,
      run_id:     typeof data.run_id === "number" ? data.run_id : undefined,
      workflow:   typeof data.workflow === "string" ? data.workflow : undefined,
      status:     typeof data.status === "string" ? data.status : undefined,
      conclusion: typeof data.conclusion === "string" ? data.conclusion : undefined,
      head_sha:   typeof data.head_sha === "string" ? data.head_sha : undefined,
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
        <Kpi label={t("dashboard.kpi.active_model")} value="—" hint={t("dashboard.kpi.not_trained")} />
        <Kpi label={t("dashboard.kpi.strategy")} value="—" hint={t("dashboard.kpi.not_configured")} />
        <Kpi
          label={t("dashboard.kpi.live_feed")}
          value={connected ? t("common.online") : t("common.offline")}
          hint={connected ? t("dashboard.kpi.ws_connected") : t("dashboard.kpi.reconnecting")}
        />
        <Kpi label={t("dashboard.kpi.recent_events")} value={String(events.length)} />
      </section>

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
        gridTemplateColumns: "100px 90px 1fr 1fr 80px",
        alignItems: "center",
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
      <span className="mono caps" style={{ fontSize: 11, color: "var(--text-tertiary)" }}>
        {action}
      </span>
    </div>
  );
}

const sectionTitleStyle: React.CSSProperties = {
  fontSize: "var(--fs-16)",
  fontWeight: 500,
  margin: "0 0 var(--s-3)",
};
