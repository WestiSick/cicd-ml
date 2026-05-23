import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { Button } from "@/components/Button";
import { StatusChip } from "@/components/StatusChip";
import { ApiError } from "@/api/client";
import { fetchActivity, fetchSystemHealth, listCalibrations, listWebhookEvents, pauseBGRunner, resumeBGRunner } from "@/api/admin";
import { fetchSystemState, saveAdminSettings, type CustomWeights } from "@/api/system";
import { useT } from "@/i18n";
import { formatRelativeTime } from "@/lib/format";

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

  const activity = useQuery({
    queryKey: ["admin-activity"],
    queryFn: () => fetchActivity(100),
    refetchInterval: 10_000,
  });

  return (
    <>
      <PageHeader
        title={t("admin.title")}
        subtitle={t("admin.subtitle")}
      />

      <h2 id="settings" style={sectionTitleStyle}>{t("admin.settings")}</h2>
      <SettingsBlock />

      <h2 id="system-health" style={{ ...sectionTitleStyle, marginTop: "var(--s-8)" }}>{t("admin.system_health")}</h2>
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
              <span style={{ flex: 1 }} />
              <BGRunnerToggle />
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

      <h2 id="activity" style={{ ...sectionTitleStyle, marginTop: "var(--s-8)" }}>
        {t("admin.activity")}
      </h2>
      <Card>
        {activity.isLoading && <p style={mutedText}>{t("common.loading")}</p>}
        {activity.data && activity.data.length === 0 && (
          <p style={mutedText}>{t("admin.activity.empty")}</p>
        )}
        {activity.data && activity.data.length > 0 && (
          <table style={tableStyle}>
            <thead>
              <tr>
                <Th>{t("admin.activity.col.time")}</Th>
                <Th>{t("admin.activity.col.actor")}</Th>
                <Th>{t("admin.activity.col.action")}</Th>
                <Th>{t("admin.activity.col.target")}</Th>
                <Th>{t("admin.activity.col.result")}</Th>
              </tr>
            </thead>
            <tbody>
              {activity.data.slice(0, 50).map((e) => (
                <tr key={e.id} style={{ borderTop: "1px solid var(--border-subtle)" }}>
                  <Td mono small>
                    <span title={e.at}>{formatRelativeTime(e.at)}</span>
                  </Td>
                  <Td mono>{e.actor ?? "system"}</Td>
                  <Td mono>{e.action}</Td>
                  <Td mono small>{e.target ?? "—"}</Td>
                  <Td>
                    <StatusChip status={e.success ? "done" : "failed"} />
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
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

      <h2 id="calibrations" style={{ ...sectionTitleStyle, marginTop: "var(--s-8)" }}>
        {t("admin.calibrations")}
      </h2>
      <CalibrationsCard />
    </>
  );
}

/* CalibrationsCard — per-(repo, workflow) bias multipliers. EMA-updated
 * on every workflow_run.completed webhook. Factor far from 1.0 means
 * the model is consistently off for that slice; predict applies this
 * silently when n_observations ≥ 3.
 *
 * Sorted by |1 − factor| descending so the most-corrected slices float
 * to the top — operator's attention goes where the bias is largest.
 */
function CalibrationsCard() {
  const t = useT();
  const q = useQuery({
    queryKey: ["calibrations"],
    queryFn: listCalibrations,
    refetchInterval: 10_000,
  });

  const rows = useMemo(() => {
    const data = q.data ?? [];
    return [...data].sort((a, b) => Math.abs(1 - b.factor) - Math.abs(1 - a.factor));
  }, [q.data]);

  return (
    <Card>
      <p
        style={{
          color: "var(--text-tertiary)",
          fontSize: "var(--fs-12)",
          margin: "0 0 var(--s-3) 0",
          maxWidth: 760,
          lineHeight: 1.5,
        }}
      >
        {t("admin.calibrations.hint")}
      </p>
      {q.isLoading && <p style={mutedText}>{t("common.loading")}</p>}
      {q.data && q.data.length === 0 && (
        <p style={mutedText}>{t("admin.calibrations.empty")}</p>
      )}
      {rows.length > 0 && (
        <table style={tableStyle}>
          <thead>
            <tr>
              <Th>{t("admin.calibrations.col.repo")}</Th>
              <Th>{t("admin.calibrations.col.workflow")}</Th>
              <Th right>{t("admin.calibrations.col.factor")}</Th>
              <Th right>{t("admin.calibrations.col.n")}</Th>
              <Th right>{t("admin.calibrations.col.last_actual")}</Th>
              <Th right>{t("admin.calibrations.col.last_predicted")}</Th>
              <Th right>{t("admin.calibrations.col.last_ratio")}</Th>
              <Th>{t("admin.calibrations.col.updated")}</Th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const dev = Math.abs(1 - r.factor);
              const factorColor = dev < 0.1
                ? "var(--text-secondary)"
                : dev < 0.3 ? "var(--warn)"
                : "var(--err)";
              return (
                <tr key={`${r.repo_id}|${r.workflow_name}`} style={{ borderTop: "1px solid var(--border-subtle)" }}>
                  <Td mono>{`${r.owner}/${r.name}`}</Td>
                  <Td mono small>{r.workflow_name}</Td>
                  <Td right mono><span style={{ color: factorColor, fontWeight: 500 }}>{r.factor.toFixed(2)}×</span></Td>
                  <Td right mono>{r.n_observations}</Td>
                  <Td right mono>{r.last_actual_sec !== undefined ? `${r.last_actual_sec.toFixed(1)}s` : "—"}</Td>
                  <Td right mono>{r.last_predicted_sec !== undefined ? `${r.last_predicted_sec.toFixed(1)}s` : "—"}</Td>
                  <Td right mono>{r.last_ratio !== undefined ? `${r.last_ratio.toFixed(2)}×` : "—"}</Td>
                  <Td mono small>{new Date(r.updated_at).toISOString().slice(0, 16).replace("T", " ")}</Td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </Card>
  );
}

/* BGRunnerToggle — pause/resume the in-process bg-jobs runner without
 * restarting any containers. Reflects the "paused" chip we surface on
 * /admin/health and matches what's in the system_state row.
 *
 * Polls the health endpoint to derive the current state — sufficient
 * because the operator only flips this once in a long while, not in
 * tight loops.
 */
function BGRunnerToggle() {
  const t = useT();
  const qc = useQueryClient();
  const healthQ = useQuery({ queryKey: ["system-health"], queryFn: fetchSystemHealth });
  // Paused = the health response has the "bg-jobs runner" paused chip.
  const paused = !!healthQ.data?.components.some(
    (c) => c.name === "bg-jobs runner" && c.message?.startsWith("paused"),
  );

  const pause = useMutation({
    mutationFn: pauseBGRunner,
    onSuccess: () => {
      toast.success(t("admin.bg_pause_toast"));
      qc.invalidateQueries({ queryKey: ["system-health"] });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("pause failed");
    },
  });
  const resume = useMutation({
    mutationFn: resumeBGRunner,
    onSuccess: () => {
      toast.success(t("admin.bg_resume_toast"));
      qc.invalidateQueries({ queryKey: ["system-health"] });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("resume failed");
    },
  });

  return paused ? (
    <Button variant="primary" size="sm" onClick={() => resume.mutate()} loading={resume.isPending}>
      {t("admin.bg_resume")}
    </Button>
  ) : (
    <Button variant="ghost" size="sm" onClick={() => pause.mutate()} loading={pause.isPending}>
      {t("admin.bg_pause")}
    </Button>
  );
}

/* SettingsBlock — the user-facing controls that change scheduler/ML
 * behaviour: active strategy, custom weights, GitHub PAT.
 *
 * State management: the form is initialised from /api/system/state on
 * mount, edited locally, and pushed to /api/admin/settings on Save. We
 * don't bother with optimistic updates — settings change infrequently
 * and a slight delay between Save and re-render is acceptable. */
function SettingsBlock() {
  const t = useT();
  const qc = useQueryClient();
  const sysQ = useQuery({ queryKey: ["system-state"], queryFn: fetchSystemState });

  const [strategy, setStrategy] = useState<string>("");
  const [weights, setWeights] = useState<CustomWeights>({
    short_job: 1, deadline_proximity: 0.5, branch_importance: 0.3,
  });
  const [token, setToken] = useState<string>("");
  const [touched, setTouched] = useState<{ strategy: boolean; weights: boolean; token: boolean }>({
    strategy: false, weights: false, token: false,
  });

  useEffect(() => {
    if (sysQ.data) {
      if (!touched.strategy) setStrategy(sysQ.data.active_strategy ?? "fifo");
      if (!touched.weights) setWeights(sysQ.data.custom_weights);
    }
    // We intentionally don't pre-fill `token` — the API never returns it.
    // The placeholder shows "(saved)" when one already exists.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sysQ.data]);

  const save = useMutation({
    mutationFn: () =>
      saveAdminSettings({
        active_strategy: touched.strategy ? strategy : undefined,
        custom_weights:  touched.weights  ? weights  : undefined,
        github_token:    touched.token    ? token    : undefined,
      }),
    onSuccess: () => {
      toast.success(t("admin.settings.toast.saved"));
      setTouched({ strategy: false, weights: false, token: false });
      qc.invalidateQueries({ queryKey: ["system-state"] });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("save failed");
    },
  });

  return (
    <Card>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-6)" }}>
        <div>
          <div className="caps" style={fieldLabel}>{t("admin.settings.strategy")}</div>
          <select
            value={strategy}
            onChange={(e) => { setStrategy(e.target.value); setTouched((u) => ({ ...u, strategy: true })); }}
            style={selectStyle}
          >
            {["fifo", "sjf", "edf", "custom"].map((s) => (
              <option key={s} value={s}>{s.toUpperCase()}</option>
            ))}
          </select>
          <div style={hintStyle}>{t("admin.settings.strategy.hint")}</div>
        </div>

        <div>
          <div className="caps" style={fieldLabel}>
            {t("admin.settings.token")}
            {sysQ.data && !touched.token && (
              <span style={{ marginLeft: 8, color: "var(--ok)", fontSize: "var(--fs-11)" }}>
                ({t("admin.settings.token.set")})
              </span>
            )}
          </div>
          <input
            type="password"
            value={token}
            onChange={(e) => { setToken(e.target.value); setTouched((u) => ({ ...u, token: true })); }}
            placeholder={t("admin.settings.token.placeholder")}
            spellCheck={false}
            style={inputStyle}
          />
          <div style={hintStyle}>{t("admin.settings.token.hint")}</div>
        </div>
      </div>

      <div style={{ marginTop: "var(--s-6)" }}>
        <div className="caps" style={fieldLabel}>{t("admin.settings.weights")}</div>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: "var(--s-3)" }}>
          <WeightInput
            label={t("admin.settings.weights.short_job")}
            value={weights.short_job}
            onChange={(v) => { setWeights((w) => ({ ...w, short_job: v })); setTouched((u) => ({ ...u, weights: true })); }}
          />
          <WeightInput
            label={t("admin.settings.weights.deadline")}
            value={weights.deadline_proximity}
            onChange={(v) => { setWeights((w) => ({ ...w, deadline_proximity: v })); setTouched((u) => ({ ...u, weights: true })); }}
          />
          <WeightInput
            label={t("admin.settings.weights.branch")}
            value={weights.branch_importance}
            onChange={(v) => { setWeights((w) => ({ ...w, branch_importance: v })); setTouched((u) => ({ ...u, weights: true })); }}
          />
        </div>
        <div style={hintStyle}>{t("admin.settings.weights.hint")}</div>
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "var(--s-4)" }}>
        <Button
          variant="primary"
          onClick={() => save.mutate()}
          loading={save.isPending}
          disabled={!touched.strategy && !touched.weights && !touched.token}
        >
          {t("admin.settings.save")}
        </Button>
      </div>
    </Card>
  );
}

function WeightInput({ label, value, onChange }: { label: string; value: number; onChange: (v: number) => void }) {
  return (
    <label style={{ display: "block" }}>
      <div className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-secondary)", marginBottom: 4 }}>
        {label}
      </div>
      <input
        type="number"
        step="0.1"
        min="0"
        value={value}
        onChange={(e) => onChange(Number(e.target.value) || 0)}
        style={inputStyle}
      />
    </label>
  );
}

const fieldLabel: React.CSSProperties = { color: "var(--text-tertiary)", marginBottom: "var(--s-2)" };
const hintStyle: React.CSSProperties = { color: "var(--text-tertiary)", fontSize: "var(--fs-12)", marginTop: 6, maxWidth: 480 };
const inputStyle: React.CSSProperties = {
  width: "100%",
  height: 32,
  background: "var(--bg-base)",
  border: "1px solid var(--border-subtle)",
  borderRadius: "var(--r-6)",
  padding: "0 var(--s-3)",
  color: "var(--text-primary)",
  fontFamily: "var(--font-mono)",
  fontSize: "var(--fs-13)",
  outline: "none",
};
const selectStyle: React.CSSProperties = { ...inputStyle, height: 34 };

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

function Th({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <th
      className="caps"
      style={{
        textAlign: right ? "right" : "left",
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
  right,
}: {
  children: React.ReactNode;
  mono?: boolean;
  small?: boolean;
  right?: boolean;
}) {
  return (
    <td
      className={mono ? "mono" : undefined}
      style={{
        padding: "var(--s-2) var(--s-1)",
        fontSize: small ? "var(--fs-12)" : undefined,
        textAlign: right ? "right" : undefined,
      }}
    >
      {children}
    </td>
  );
}
