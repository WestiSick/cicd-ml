import { useQuery } from "@tanstack/react-query";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { StatusChip } from "@/components/StatusChip";
import { fetchSystemHealth, listWebhookEvents } from "@/api/admin";
import { useT } from "@/i18n";

/* /admin — operational diagnostics, not user-facing settings.
 *
 * Sections (anchor links from elsewhere in the app):
 *   #system-health  — per-service status, polled every 10s
 *   #webhooks       — last 50 GitHub deliveries with HMAC outcome
 *   #github         — token configuration (future)
 *
 * The page deliberately renders even when services are down — that's the
 * whole point of looking at it. Error states inside the sections rather
 * than a global blocker. */
export function Admin() {
  const t = useT();
  const health = useQuery({
    queryKey: ["system-health"],
    queryFn: fetchSystemHealth,
    refetchInterval: 10_000,
  });

  const webhooks = useQuery({
    queryKey: ["admin-webhooks"],
    queryFn: () => listWebhookEvents(50),
    refetchInterval: 15_000,
  });

  return (
    <>
      <PageHeader
        title={t("admin.title")}
        subtitle={t("admin.subtitle")}
      />

      <h2 id="system-health" style={sectionTitleStyle}>{t("admin.system_health")}</h2>
      <Card>
        {health.isLoading && <p style={mutedText}>{t("common.loading")}</p>}
        {health.isError && <p style={mutedText}>{t("common.retry")}</p>}
        {health.data && (
          <>
            <div style={{ display: "flex", alignItems: "center", gap: "var(--s-3)", marginBottom: "var(--s-3)" }}>
              <StatusChip status={health.data.state === "ok" ? "synced" : health.data.state === "degraded" ? "paused" : "failed"} />
              <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-tertiary)" }}>
                {t("admin.health.last_checked", { time: new Date(health.data.time).toLocaleTimeString() })}
              </span>
            </div>
            <div style={{ display: "grid", gap: "var(--s-2)" }}>
              {health.data.components.map((c) => (
                <div
                  key={c.name}
                  style={{
                    display: "grid",
                    gridTemplateColumns: "180px 90px 1fr",
                    alignItems: "center",
                    padding: "6px 0",
                    borderTop: "1px solid var(--border-subtle)",
                  }}
                >
                  <span className="mono" style={{ fontSize: "var(--fs-13)" }}>{c.name}</span>
                  <StatusChip status={c.state === "ok" ? "synced" : c.state === "degraded" ? "paused" : "failed"} />
                  <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-12)" }}>{c.message}</span>
                </div>
              ))}
            </div>
          </>
        )}
      </Card>

      <h2 id="webhooks" style={{ ...sectionTitleStyle, marginTop: "var(--s-8)" }}>
        {t("admin.webhooks")}
      </h2>
      <Card>
        {webhooks.isLoading && <p style={mutedText}>{t("common.loading")}</p>}
        {webhooks.data && webhooks.data.length === 0 && (
          <p style={mutedText}>{t("admin.webhooks_empty")}</p>
        )}
        {webhooks.data && webhooks.data.length > 0 && (
          <table style={tableStyle}>
            <thead>
              <tr>
                <Th>{t("admin.webhook.col.received")}</Th>
                <Th>{t("admin.webhook.col.event")}</Th>
                <Th>{t("admin.webhook.col.repo")}</Th>
                <Th>{t("admin.webhook.col.hmac")}</Th>
                <Th>{t("admin.webhook.col.error")}</Th>
              </tr>
            </thead>
            <tbody>
              {webhooks.data.map((e) => (
                <tr key={e.id} style={{ borderTop: "1px solid var(--border-subtle)" }}>
                  <Td mono>{new Date(e.received_at).toISOString().slice(11, 19)}</Td>
                  <Td mono>{e.event_type ?? "—"}</Td>
                  <Td mono>{e.repo ?? "—"}</Td>
                  <Td>{e.hmac_valid === undefined ? "—" : e.hmac_valid ? "✓" : "✗"}</Td>
                  <Td mono small>{e.error ?? ""}</Td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
    </>
  );
}

const sectionTitleStyle: React.CSSProperties = {
  fontSize: "var(--fs-16)",
  fontWeight: 500,
  margin: "0 0 var(--s-3)",
};

const mutedText: React.CSSProperties = {
  color: "var(--text-secondary)",
  fontSize: "var(--fs-13)",
  margin: 0,
};

const tableStyle: React.CSSProperties = {
  width: "100%",
  borderCollapse: "collapse",
  fontSize: "var(--fs-13)",
};

function Th({ children }: { children: React.ReactNode }) {
  return (
    <th
      className="caps"
      style={{
        textAlign: "left",
        padding: "var(--s-2) var(--s-1)",
        color: "var(--text-tertiary)",
        fontWeight: 500,
      }}
    >
      {children}
    </th>
  );
}

function Td({
  children,
  mono,
  small,
}: {
  children: React.ReactNode;
  mono?: boolean;
  small?: boolean;
}) {
  return (
    <td
      className={mono ? "mono" : undefined}
      style={{
        padding: "var(--s-2) var(--s-1)",
        fontSize: small ? "var(--fs-12)" : undefined,
      }}
    >
      {children}
    </td>
  );
}
