import { useMemo } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useQueries } from "@tanstack/react-query";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { EmptyState } from "@/components/EmptyState";
import { listModels, type ModelRow } from "@/api/models";
import { getFeatureImportance, getPredictedVsActual, type FeatureImportance, type PredictedActualPoint } from "@/api/modelDetail";
import { useT } from "@/i18n";
import { formatDuration } from "@/lib/format";

/* /experiments/compare?ids=1,2,3 — head-to-head comparison page.
 *
 * Three blocks:
 *   1. Metrics table — one column per model, rows = MAE/RMSE/MAPE/R²/
 *      Spearman/NDCG@k. Best value per row is bolded.
 *   2. Overlaid predicted-vs-actual scatter — points coloured by model,
 *      same log-log axes as the per-model scatter. Diagonal reference.
 *   3. Top-features bar chart — top-10 feature names by combined
 *      importance across selected models, with one bar per model.
 *
 * Backed by parallel fetches of /api/models/{id} variants. Limited to 5
 * models per comparison (URL gets unwieldy beyond that, scatter starts
 * to lose its punch with too many overlapping series).
 */
export function ModelCompare() {
  const t = useT();
  const [params] = useSearchParams();
  const idsRaw = params.get("ids") ?? "";
  const ids = useMemo(
    () =>
      idsRaw
        .split(",")
        .map((s) => Number(s.trim()))
        .filter((n) => Number.isFinite(n) && n > 0)
        .slice(0, 5),
    [idsRaw],
  );

  const queries = useQueries({
    queries: [
      {
        queryKey: ["models"],
        queryFn: listModels,
      },
      ...ids.map((id) => ({
        queryKey: ["compare", id, "pva"],
        queryFn: () => getPredictedVsActual(id, 1000),
      })),
      ...ids.map((id) => ({
        queryKey: ["compare", id, "fi"],
        queryFn: () => getFeatureImportance(id, 30),
      })),
    ],
  });

  const allModels = (queries[0].data as ModelRow[] | undefined) ?? [];
  const selectedModels = ids
    .map((id) => allModels.find((m) => m.id === id))
    .filter((m): m is ModelRow => !!m);

  if (ids.length === 0) {
    return (
      <>
        <PageHeader
          title={t("compare.title")}
          subtitle={t("compare.subtitle")}
          actions={<Link to="/experiments">{t("compare.back")}</Link>}
        />
        <EmptyState
          title={t("compare.empty.title")}
          hint={t("compare.empty.hint")}
        />
      </>
    );
  }

  const isLoading = queries.some((q) => q.isLoading);

  // PvA series: each model = one coloured series of points. Stored as a
  // flat array tagged with modelId so the SVG renderer can colour-code.
  const pvaByModel: Array<{ id: number; algo: string; points: PredictedActualPoint[] }> = ids.map((id, i) => ({
    id,
    algo: selectedModels[i]?.algo ?? `model_${id}`,
    points: (queries[1 + i].data as PredictedActualPoint[] | undefined) ?? [],
  }));

  // Importance: collect top-10 unique feature names across all selected
  // models (ranked by max importance across models), then for each name
  // build the per-model value list.
  const fiByModel: Array<{ id: number; algo: string; items: FeatureImportance[] }> = ids.map((id, i) => ({
    id,
    algo: selectedModels[i]?.algo ?? `model_${id}`,
    items: (queries[1 + ids.length + i].data as FeatureImportance[] | undefined) ?? [],
  }));

  const featureRanks = combineFeatureRanks(fiByModel, 10);

  return (
    <>
      <PageHeader
        title={t("compare.title")}
        subtitle={`${selectedModels.length} ${t("compare.models_selected")}`}
        actions={<Link to="/experiments" style={{ color: "var(--text-secondary)" }}>{t("compare.back")}</Link>}
      />

      {isLoading && <p style={{ color: "var(--text-secondary)" }}>{t("common.loading")}</p>}

      {/* === Metrics table === */}
      <Card>
        <SectionTitle>{t("compare.metrics")}</SectionTitle>
        <table style={tableStyle}>
          <thead>
            <tr>
              <Th>metric</Th>
              {selectedModels.map((m) => (
                <Th key={m.id} right>
                  #{m.id} <span style={{ color: "var(--text-tertiary)" }}>{m.algo}</span>
                </Th>
              ))}
            </tr>
          </thead>
          <tbody>
            {METRIC_ROWS.map((row) => {
              const values = selectedModels.map((m) => num(m.metrics?.[row.key]));
              const best = bestIndex(values, row.lowerIsBetter);
              return (
                <tr key={row.key}>
                  <Td>{row.label}</Td>
                  {values.map((v, i) => (
                    <Td key={i} right mono bold={i === best && v !== undefined}>
                      {v === undefined ? "—" : row.format(v)}
                    </Td>
                  ))}
                </tr>
              );
            })}
          </tbody>
        </table>
      </Card>

      {/* === Overlaid PvA scatter === */}
      <div style={{ marginTop: "var(--s-3)" }}>
        <Card>
          <SectionTitle>{t("compare.scatter")}</SectionTitle>
          <OverlaidScatter series={pvaByModel} width={760} height={420} />
          <Legend models={pvaByModel.map((s) => ({ id: s.id, algo: s.algo }))} />
        </Card>
      </div>

      {/* === Feature importance overlay === */}
      <div style={{ marginTop: "var(--s-3)" }}>
        <Card>
          <SectionTitle>{t("compare.features")}</SectionTitle>
          {featureRanks.length === 0 ? (
            <p style={{ color: "var(--text-secondary)", fontSize: "var(--fs-13)" }}>
              {t("compare.features.empty")}
            </p>
          ) : (
            <FeatureBars
              ranks={featureRanks}
              models={fiByModel.map((f) => ({ id: f.id, algo: f.algo }))}
            />
          )}
        </Card>
      </div>
    </>
  );
}

// ---- helpers ---------------------------------------------------------

type MetricRow = {
  key: string;
  label: string;
  lowerIsBetter: boolean;
  format: (v: number) => string;
};

const METRIC_ROWS: MetricRow[] = [
  { key: "mae_test_sec",  label: "MAE test (s)",  lowerIsBetter: true,  format: (v) => formatDuration(v) },
  { key: "rmse_test_sec", label: "RMSE test (s)", lowerIsBetter: true,  format: (v) => formatDuration(v) },
  { key: "mape_test",     label: "MAPE",          lowerIsBetter: true,  format: (v) => v.toFixed(3) },
  { key: "r2_test",       label: "R²",            lowerIsBetter: false, format: (v) => v.toFixed(3) },
  { key: "spearman_test", label: "Spearman ρ",    lowerIsBetter: false, format: (v) => v.toFixed(3) },
  { key: "ndcg_at_10",    label: "NDCG@10",       lowerIsBetter: false, format: (v) => v.toFixed(3) },
  { key: "ndcg_at_50",    label: "NDCG@50",       lowerIsBetter: false, format: (v) => v.toFixed(3) },
  { key: "ndcg_at_100",   label: "NDCG@100",      lowerIsBetter: false, format: (v) => v.toFixed(3) },
];

function num(v: number | undefined): number | undefined {
  if (typeof v !== "number" || !isFinite(v)) return undefined;
  return v;
}

function bestIndex(values: (number | undefined)[], lowerIsBetter: boolean): number {
  let best = -1;
  let bestV: number | undefined = undefined;
  for (let i = 0; i < values.length; i++) {
    const v = values[i];
    if (v === undefined) continue;
    if (bestV === undefined || (lowerIsBetter ? v < bestV : v > bestV)) {
      bestV = v;
      best = i;
    }
  }
  return best;
}

// Distinct colours per model series — kept small (5) to match the
// max-models cap. Drawn from the design palette so it harmonises with the
// rest of the app (warm accent + cool info + status palette).
const SERIES_COLOURS = [
  "var(--accent)",  // amber 1
  "var(--info)",    // blue 2
  "var(--ok)",      // green 3
  "var(--warn)",    // orange 4
  "var(--err)",     // red 5
];

function combineFeatureRanks(
  fi: Array<{ id: number; items: FeatureImportance[] }>,
  topK: number,
): Array<{ name: string; values: (number | undefined)[] }> {
  // For each feature name find the MAX importance across models — used
  // to pick top-K most-important-anywhere features. Then for each
  // surviving name, build the per-model value list (undefined if a
  // model didn't include this feature in its top-30).
  const maxByName = new Map<string, number>();
  for (const m of fi) {
    for (const it of m.items) {
      const cur = maxByName.get(it.name) ?? 0;
      if (it.value > cur) maxByName.set(it.name, it.value);
    }
  }
  const sortedNames = Array.from(maxByName.entries())
    .sort((a, b) => b[1] - a[1])
    .slice(0, topK)
    .map(([name]) => name);

  return sortedNames.map((name) => ({
    name,
    values: fi.map((m) => m.items.find((it) => it.name === name)?.value),
  }));
}

// ---- inline charts ---------------------------------------------------

function OverlaidScatter({
  series,
  width,
  height,
}: {
  series: Array<{ id: number; algo: string; points: PredictedActualPoint[] }>;
  width: number;
  height: number;
}) {
  const margin = { top: 12, right: 16, bottom: 32, left: 50 };
  const innerW = width - margin.left - margin.right;
  const innerH = height - margin.top - margin.bottom;

  // Log-log scale matching the per-model ScatterPlot — covers the
  // 4-orders-of-magnitude range of CI durations.
  let lo = Infinity, hi = -Infinity;
  for (const s of series) {
    for (const p of s.points) {
      if (p.actual_sec > 0 && p.actual_sec < lo) lo = p.actual_sec;
      if (p.predicted_sec > 0 && p.predicted_sec < lo) lo = p.predicted_sec;
      if (p.actual_sec > hi) hi = p.actual_sec;
      if (p.predicted_sec > hi) hi = p.predicted_sec;
    }
  }
  if (!isFinite(lo)) { lo = 1; hi = 100; }
  if (lo <= 0) lo = 1;
  if (hi <= lo) hi = lo * 10;
  const logLo = Math.log10(lo);
  const logHi = Math.log10(hi);

  const xs = (v: number) => ((Math.log10(v) - logLo) / (logHi - logLo)) * innerW;
  const ys = (v: number) => innerH - ((Math.log10(v) - logLo) / (logHi - logLo)) * innerH;

  return (
    <svg width={width} height={height} role="img" aria-label="overlaid predicted vs actual">
      <g transform={`translate(${margin.left}, ${margin.top})`}>
        {/* Diagonal reference y = x */}
        <line
          x1={xs(lo)} y1={ys(lo)} x2={xs(hi)} y2={ys(hi)}
          stroke="var(--border-strong)" strokeDasharray="4 4"
        />
        {series.map((s, i) =>
          s.points
            .filter((p) => p.actual_sec > 0 && p.predicted_sec > 0)
            .map((p, j) => (
              <circle
                key={`${i}-${j}`}
                cx={xs(p.actual_sec)} cy={ys(p.predicted_sec)}
                r={2}
                fill={SERIES_COLOURS[i % SERIES_COLOURS.length]}
                fillOpacity={0.5}
              />
            )),
        )}
        <text x={innerW / 2} y={innerH + 24} textAnchor="middle"
          fontFamily="var(--font-mono)" fontSize={10} fill="var(--text-tertiary)">
          actual (sec, log scale)
        </text>
        <text x={-innerH / 2} y={-38} transform="rotate(-90)" textAnchor="middle"
          fontFamily="var(--font-mono)" fontSize={10} fill="var(--text-tertiary)">
          predicted (sec, log scale)
        </text>
      </g>
    </svg>
  );
}

function Legend({ models }: { models: Array<{ id: number; algo: string }> }) {
  return (
    <div style={{ display: "flex", gap: "var(--s-3)", flexWrap: "wrap", marginTop: "var(--s-2)" }}>
      {models.map((m, i) => (
        <span key={m.id} className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-secondary)" }}>
          <span style={{ display: "inline-block", width: 10, height: 10, background: SERIES_COLOURS[i % SERIES_COLOURS.length], marginRight: 6, borderRadius: 2 }} />
          #{m.id} {m.algo}
        </span>
      ))}
    </div>
  );
}

function FeatureBars({
  ranks,
  models,
}: {
  ranks: Array<{ name: string; values: (number | undefined)[] }>;
  models: Array<{ id: number; algo: string }>;
}) {
  // Per-row max so we can scale each row independently — feature
  // importance values aren't comparable across rows anyway, only across
  // models within the same row.
  return (
    <div style={{ display: "grid", gap: "var(--s-2)" }}>
      {ranks.map((row) => {
        const max = Math.max(0.0001, ...row.values.filter((v): v is number => v !== undefined));
        return (
          <div key={row.name} style={{ display: "grid", gridTemplateColumns: "200px 1fr", gap: "var(--s-2)", alignItems: "center" }}>
            <span className="mono" style={{ fontSize: "var(--fs-12)" }} title={row.name}>
              {row.name.length > 28 ? row.name.slice(0, 26) + "…" : row.name}
            </span>
            <div style={{ display: "grid", gap: 2 }}>
              {row.values.map((v, i) => (
                <div key={i} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <div
                    style={{
                      height: 8,
                      width: `${v === undefined ? 0 : (v / max) * 100}%`,
                      background: SERIES_COLOURS[i % SERIES_COLOURS.length],
                      borderRadius: 2,
                      transition: "width var(--t-entry) var(--ease)",
                    }}
                  />
                  <span className="mono" style={{ fontSize: 10, color: "var(--text-tertiary)" }}>
                    {v === undefined ? "—" : v.toFixed(3)} <span style={{ marginLeft: 6 }}>{models[i].algo}</span>
                  </span>
                </div>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)", fontSize: "var(--fs-12)" }}>
      {children}
    </div>
  );
}

const tableStyle: React.CSSProperties = { width: "100%", borderCollapse: "collapse", fontSize: "var(--fs-13)" };
function Th({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <th
      style={{
        textAlign: right ? "right" : "left",
        padding: "6px 8px",
        borderBottom: "1px solid var(--border-subtle)",
        color: "var(--text-tertiary)",
        fontWeight: 500,
        textTransform: "uppercase",
        letterSpacing: "0.06em",
        fontFamily: "var(--font-mono)",
        fontSize: 11,
      }}
    >
      {children}
    </th>
  );
}
function Td({ children, right, mono, bold }: { children: React.ReactNode; right?: boolean; mono?: boolean; bold?: boolean }) {
  return (
    <td
      className={mono ? "mono" : undefined}
      style={{
        textAlign: right ? "right" : "left",
        padding: "6px 8px",
        borderBottom: "1px solid var(--border-subtle)",
        fontWeight: bold ? 600 : 400,
        color: bold ? "var(--accent)" : "var(--text-primary)",
      }}
    >
      {children}
    </td>
  );
}
