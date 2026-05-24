import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { EmptyState } from "@/components/EmptyState";
import { StatusChip } from "@/components/StatusChip";
import { fetchQueueHistory, type HistoryFilters, type HistoryRow } from "@/api/queue";
import { listRepos } from "@/api/repos";
import { useT } from "@/i18n";
import { formatDuration } from "@/lib/format";

/* /history — persistent log of webhook (predicted, actual, δ%) tuples.
 *
 * Backs the "where can I see past pushes" need that /dashboard's live
 * feed couldn't: dashboard events live only in browser memory and die
 * on reload. This page is server-backed (prediction_log table),
 * filterable, and shows full calibration math.
 *
 * Aggregate KPIs at the top — count, mean |δ|, median |δ|, fraction
 * within ±20% — give a single-glance health read on the current model.
 * Useful for the thesis to demo "system is converging" via screenshots
 * taken at different times.
 */

/* Window presets — `value` is hours.
 *
 * "all" maps to 0 which the backend interprets as no time filter
 * (returns rows newest-first up to `limit`). Used for the thesis
 * demo's "show me everything since February" view.
 */
const HOURS_PRESETS: Array<{ label: string; value: number }> = [
  { label: "24h",  value: 24 },
  { label: "7d",   value: 168 },
  { label: "30d",  value: 720 },
  { label: "90d",  value: 2160 },
  { label: "180d", value: 4320 },
  { label: "all",  value: 0 },
];

const DELTA_PRESETS: Array<{ label: string; value: number }> = [
  { label: "all",   value: 0  },
  { label: "≥10%", value: 10 },
  { label: "≥30%", value: 30 },
  { label: "≥50%", value: 50 },
];

export function History() {
  const t = useT();

  const [hours, setHours] = useState(168);
  const [minAbsDelta, setMinAbsDelta] = useState(0);
  const [repo, setRepo] = useState<string>("");
  const [limit, setLimit] = useState(100);

  const filters: HistoryFilters = useMemo(
    () => ({ hours, minAbsDelta, repo: repo || undefined, limit }),
    [hours, minAbsDelta, repo, limit],
  );

  const q = useQuery({
    queryKey: ["queue-history", filters],
    queryFn: () => fetchQueueHistory(filters),
    refetchInterval: 10_000,
  });

  const reposQ = useQuery({ queryKey: ["repos"], queryFn: listRepos });
  const repoSlugs = useMemo(
    () => (reposQ.data ?? []).map((r) => `${r.owner}/${r.name}`).sort(),
    [reposQ.data],
  );

  const stats = useMemo(() => computeStats(q.data ?? []), [q.data]);

  return (
    <>
      <PageHeader
        title={t("history.title")}
        subtitle={t("history.subtitle")}
      />

      {/* KPIs */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(4, 1fr)",
          gap: "var(--s-3)",
          marginBottom: "var(--s-4)",
        }}
      >
        <KPI label={t("history.kpi.count")}  value={stats.count.toLocaleString()} />
        <KPI label={t("history.kpi.mean")}   value={fmtPct(stats.meanAbs)}        accent={stats.count > 0} />
        <KPI label={t("history.kpi.median")} value={fmtPct(stats.medianAbs)}      accent={stats.count > 0} />
        <KPI label={t("history.kpi.in20")}   value={stats.count > 0 ? `${Math.round(stats.in20 * 100)}%` : "—"} accent={stats.count > 0} />
      </div>

      {/* Filters */}
      <Card style={{ marginBottom: "var(--s-3)" }}>
        <div style={{ display: "flex", flexWrap: "wrap", gap: "var(--s-3)", alignItems: "center" }}>
          <FilterGroup label={t("history.filter.window")}>
            {HOURS_PRESETS.map((p) => (
              <Pill key={p.value} active={hours === p.value} onClick={() => setHours(p.value)}>
                {p.label}
              </Pill>
            ))}
          </FilterGroup>
          <FilterGroup label={t("history.filter.delta")}>
            {DELTA_PRESETS.map((p) => (
              <Pill key={p.value} active={minAbsDelta === p.value} onClick={() => setMinAbsDelta(p.value)}>
                {p.label}
              </Pill>
            ))}
          </FilterGroup>
          <FilterGroup label={t("history.filter.repo")}>
            <select
              value={repo}
              onChange={(e) => setRepo(e.target.value)}
              style={selectStyle}
            >
              <option value="">{t("history.filter.repo.all")}</option>
              {repoSlugs.map((slug) => (
                <option key={slug} value={slug}>{slug}</option>
              ))}
            </select>
          </FilterGroup>
          <FilterGroup label={t("history.filter.limit")}>
            {[100, 500, 1000, 2000].map((n) => (
              <Pill key={n} active={limit === n} onClick={() => setLimit(n)}>
                {n}
              </Pill>
            ))}
          </FilterGroup>
        </div>
      </Card>

      <Card>
        {q.isLoading && <p style={mutedText}>{t("common.loading")}</p>}
        {q.data && q.data.length === 0 && (
          <EmptyState
            title={t("history.empty.title")}
            hint={t("history.empty.hint")}
          />
        )}
        {q.data && q.data.length > 0 && (
          <div style={{ overflowX: "auto" }}>
            <table style={tableStyle}>
              <thead>
                <tr>
                  <Th>{t("history.col.time")}</Th>
                  <Th>{t("history.col.repo")}</Th>
                  <Th>{t("history.col.workflow")}</Th>
                  <Th>{t("history.col.branch_sha")}</Th>
                  <Th right>{t("history.col.predicted")}</Th>
                  <Th right>{t("history.col.actual")}</Th>
                  <Th right>{t("history.col.delta")}</Th>
                  <Th right>{t("history.col.calibration")}</Th>
                  <Th>{t("history.col.model")}</Th>
                  <Th>{t("history.col.conclusion")}</Th>
                </tr>
              </thead>
              <tbody>
                {q.data.map((r) => <Row key={r.id} row={r} />)}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  );
}

/* Row — one line per completed workflow_run with the full predicted /
 * actual / δ trio + optional calibration math in a tooltip. */
function Row({ row }: { row: HistoryRow }) {
  const status = row.conclusion === "success" ? "done"
               : row.conclusion === "failure" ? "failed"
               : "queued"; // unknown / cancelled / etc

  const dColor = deltaColour(row.delta_pct);
  const calibTooltip = row.predicted_raw_sec !== undefined
    && row.calibration_factor !== undefined
    && Math.abs(row.calibration_factor - 1.0) > 0.01
      ? `raw ${formatDuration(row.predicted_raw_sec)} · ${row.calibration_factor.toFixed(2)}× → ${formatDuration(row.predicted_sec ?? row.predicted_raw_sec)}`
      : "";

  return (
    <tr style={{ borderTop: "1px solid var(--border-subtle)" }}>
      {/* Full YYYY-MM-DD HH:mm — needed for the thesis windows that
          stretch across multiple months / years. */}
      <Td mono small>{new Date(row.completed_at).toISOString().slice(0, 16).replace("T", " ")}</Td>
      <Td mono small>{row.repo}</Td>
      <Td mono small>{row.workflow ?? "—"}</Td>
      <Td mono small>
        {row.head_branch ?? "—"}
        {row.head_sha && <span style={{ color: "var(--text-tertiary)" }}> · {row.head_sha.slice(0, 7)}</span>}
      </Td>
      <Td right mono>
        <span title={calibTooltip} style={{ cursor: calibTooltip ? "help" : undefined }}>
          {row.predicted_sec !== undefined ? formatDuration(row.predicted_sec) : "—"}
          {calibTooltip && <span style={{ color: "var(--accent)", marginLeft: 3 }}>•</span>}
        </span>
      </Td>
      <Td right mono>{row.actual_sec !== undefined ? formatDuration(row.actual_sec) : "—"}</Td>
      <Td right mono>
        <span style={{ color: dColor, fontWeight: 500 }}>
          {row.delta_pct !== undefined ? fmtSignedPct(row.delta_pct) : "—"}
        </span>
      </Td>
      <Td right mono small>
        {row.calibration_factor !== undefined
          ? `${row.calibration_factor.toFixed(2)}×`
          : "—"}
      </Td>
      <Td mono small>{row.model_algo ?? "—"}{row.model_id ? ` #${row.model_id}` : ""}</Td>
      <Td><StatusChip status={status} /></Td>
    </tr>
  );
}

/* ---- stat helpers ---------------------------------------------------- */

function computeStats(rows: HistoryRow[]) {
  const abs = rows
    .map((r) => (r.delta_pct === undefined ? null : Math.abs(r.delta_pct)))
    .filter((v): v is number => v !== null)
    .sort((a, b) => a - b);
  if (abs.length === 0) {
    return { count: rows.length, meanAbs: null, medianAbs: null, in20: 0 };
  }
  const mean = abs.reduce((a, b) => a + b, 0) / abs.length;
  const median = abs[Math.floor(abs.length / 2)];
  const within20 = abs.filter((v) => v <= 20).length / abs.length;
  return { count: rows.length, meanAbs: mean, medianAbs: median, in20: within20 };
}

function fmtPct(v: number | null): string {
  if (v === null || !Number.isFinite(v)) return "—";
  return `${v.toFixed(1)}%`;
}

function fmtSignedPct(v: number): string {
  const sign = v >= 0 ? "+" : "";
  return `${sign}${v.toFixed(1)}%`;
}

function deltaColour(d: number | undefined): string {
  if (d === undefined) return "var(--text-tertiary)";
  const abs = Math.abs(d);
  if (abs <= 10) return "var(--ok)";
  if (abs <= 30) return "var(--warn)";
  return "var(--err)";
}

/* ---- small UI atoms -------------------------------------------------- */

function KPI({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <Card>
      <div className="caps" style={{ color: "var(--text-tertiary)", fontSize: "var(--fs-12)" }}>{label}</div>
      <div className="mono" style={{ fontSize: "var(--fs-28)", color: accent ? "var(--accent)" : "var(--text-primary)", marginTop: 4 }}>
        {value}
      </div>
    </Card>
  );
}

function FilterGroup({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: "inline-flex", alignItems: "center", gap: "var(--s-2)" }}>
      <span className="caps" style={{ color: "var(--text-tertiary)", fontSize: 11 }}>{label}</span>
      {children}
    </div>
  );
}

function Pill({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      style={{
        height: 28,
        padding: "0 10px",
        background: active ? "var(--bg-base)" : "transparent",
        color:      active ? "var(--text-primary)" : "var(--text-secondary)",
        border: `1px solid ${active ? "var(--border-strong)" : "var(--border-subtle)"}`,
        borderRadius: "var(--r-6)",
        fontSize: "var(--fs-12)",
        cursor: "pointer",
        fontFamily: "var(--font-mono)",
        boxShadow: active ? "inset 0 0 0 1px var(--accent-soft)" : undefined,
      }}
    >
      {children}
    </button>
  );
}

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
        borderBottom: "1px solid var(--border-subtle)",
      }}
    >
      {children}
    </th>
  );
}

function Td({ children, mono, small, right }: { children: React.ReactNode; mono?: boolean; small?: boolean; right?: boolean }) {
  return (
    <td
      className={mono ? "mono" : undefined}
      style={{
        padding: "5px 4px",
        fontSize: small ? "var(--fs-12)" : undefined,
        textAlign: right ? "right" : undefined,
        whiteSpace: "nowrap",
      }}
    >
      {children}
    </td>
  );
}

const mutedText: React.CSSProperties = { color: "var(--text-tertiary)", fontSize: "var(--fs-13)", margin: 0 };
const selectStyle: React.CSSProperties = {
  height: 28,
  padding: "0 var(--s-2)",
  background: "var(--bg-base)",
  border: "1px solid var(--border-subtle)",
  borderRadius: "var(--r-6)",
  color: "var(--text-primary)",
  fontFamily: "var(--font-mono)",
  fontSize: "var(--fs-12)",
  outline: "none",
};
