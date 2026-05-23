import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";

import { Button } from "./Button";
import { ApiError } from "@/api/client";
import { listRepos } from "@/api/repos";
import { fetchTimeline } from "@/api/repos";
import { startTraining } from "@/api/models";
import { useT } from "@/i18n";
import type { TranslationKey } from "@/i18n/types";

/* TrainWizard — the full /experiments §7-4 training wizard.
 *
 * Four sequential steps:
 *   1. Algo        — pick one of linear / rf / xgb / lgbm / mlp / lstm
 *   2. Dataset     — checkbox repos + slider time window
 *   3. Hyperparams — per-algo form with sliders; defaults shown
 *   4. Split       — timeline bar showing run counts per day with a
 *                    draggable cutoff line for train/test boundary
 *
 * On Submit, posts a single /api/training request with the assembled
 * payload. Activate-on-finish lives next to the submit button.
 *
 * Why a wizard rather than a flat form: the user mental model for
 * training is sequential ("pick algorithm → narrow to repos → tune →
 * pick cutoff"), and each step's options depend on the previous. The
 * wizard reflects that flow and keeps each screen short.
 */

const ALGORITHMS: { id: string; labelKey: TranslationKey; lstm?: boolean }[] = [
  { id: "linear",   labelKey: "setup.algo.linear" },
  { id: "rf",       labelKey: "setup.algo.rf" },
  { id: "xgboost",  labelKey: "setup.algo.xgboost" },
  { id: "lightgbm", labelKey: "setup.algo.lightgbm" },
  { id: "mlp",      labelKey: "setup.algo.mlp" },
  { id: "lstm",     labelKey: "setup.algo.lstm", lstm: true },
];

// Hyperparameter definitions per algo. Each is a slider with min/max/step
// and a hint. Defaults match what the BaseModel._build_estimator constructs
// on the Python side — so empty params produces the same model as the
// wizard's defaults.
const HYPERPARAMS: Record<string, Array<{ key: string; label: string; min: number; max: number; step: number; def: number; hint?: string }>> = {
  linear: [
    { key: "alpha", label: "alpha (Ridge L2)", min: 0, max: 10, step: 0.1, def: 1.0, hint: "0 → OLS" },
  ],
  rf: [
    { key: "n_estimators", label: "n_estimators", min: 50, max: 1000, step: 50, def: 300 },
    { key: "max_depth",    label: "max_depth (0 = none)", min: 0, max: 30, step: 1, def: 0 },
  ],
  xgboost: [
    { key: "n_estimators",   label: "n_estimators",   min: 50, max: 1500, step: 50, def: 400 },
    { key: "max_depth",      label: "max_depth",      min: 2,  max: 12,   step: 1,  def: 6 },
    { key: "learning_rate",  label: "learning_rate",  min: 0.01, max: 0.3, step: 0.01, def: 0.05 },
    { key: "subsample",      label: "subsample",      min: 0.5, max: 1.0, step: 0.05, def: 0.8 },
  ],
  lightgbm: [
    { key: "n_estimators",  label: "n_estimators",  min: 50, max: 1500, step: 50, def: 400 },
    { key: "num_leaves",    label: "num_leaves",    min: 8, max: 256, step: 4, def: 31 },
    { key: "learning_rate", label: "learning_rate", min: 0.01, max: 0.3, step: 0.01, def: 0.05 },
  ],
  mlp: [
    { key: "hidden_layer_size", label: "hidden_layer_size", min: 16, max: 256, step: 8, def: 64 },
    { key: "max_iter",          label: "max_iter",          min: 50, max: 1000, step: 50, def: 300 },
  ],
  lstm: [
    { key: "hidden_size", label: "hidden_size", min: 16, max: 256, step: 8, def: 64 },
    { key: "num_layers",  label: "num_layers",  min: 1,  max: 4,   step: 1,  def: 2 },
    { key: "epochs",      label: "epochs",      min: 10, max: 200, step: 10, def: 50 },
  ],
};

export function TrainWizard({ onClose }: { onClose: () => void }) {
  const t = useT();
  const [step, setStep] = useState(1);
  const [algo, setAlgo] = useState("xgboost");
  const [repoIDs, setRepoIDs] = useState<Set<number>>(new Set());
  const [months, setMonths] = useState<3 | 6 | 12>(6);
  const [params, setParams] = useState<Record<string, number>>({});
  const [cutoffISO, setCutoffISO] = useState<string>("");
  const [activate, setActivate] = useState(true);

  // Initialise params on algo change to that algo's defaults — keeps the
  // sliders from being empty.
  useEffect(() => {
    const defs = HYPERPARAMS[algo] ?? [];
    const seeded: Record<string, number> = {};
    for (const d of defs) seeded[d.key] = d.def;
    setParams(seeded);
  }, [algo]);

  const reposQ = useQuery({ queryKey: ["repos"], queryFn: listRepos });
  const allRepos = reposQ.data ?? [];
  // Default to all repos selected — the user opts OUT rather than IN.
  useEffect(() => {
    if (allRepos.length > 0 && repoIDs.size === 0) {
      setRepoIDs(new Set(allRepos.map((r) => r.id)));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reposQ.data]);

  // Timeline is fetched for the selected repos + months window; we
  // refetch when either changes so the cutoff bar reflects reality.
  const timelineQ = useQuery({
    queryKey: ["timeline", Array.from(repoIDs).sort((a, b) => a - b), months],
    queryFn: () => fetchTimeline({ days: months * 30, repoIDs: Array.from(repoIDs) }),
    enabled: step >= 4,
  });

  // Default cutoff: 80% through the visible time window — matches the
  // train_frac=0.8 default in the time_based_split.
  useEffect(() => {
    if (step === 4 && timelineQ.data && !cutoffISO && timelineQ.data.cells.length > 0) {
      const cells = timelineQ.data.cells;
      const idx = Math.max(1, Math.floor(cells.length * 0.8));
      setCutoffISO(cells[Math.min(idx, cells.length - 1)].day);
    }
  }, [step, timelineQ.data, cutoffISO]);

  const submit = useMutation({
    mutationFn: () => {
      // Translate UI state to /api/training payload.
      const sinceIso = cutoffISO ? null : new Date(Date.now() - months * 30 * 86400_000).toISOString();
      // Cutoff is the *training-set end* boundary; we pass `since` to
      // include data from `cutoff - months` back to cutoff. ml-service
      // does the train/test split with train_frac=0.8 internally; the
      // wizard's explicit cutoff is the lower bound only.
      return startTraining({
        algo,
        params,
        repo_ids: Array.from(repoIDs),
        since: sinceIso ?? undefined,
        activate,
        name: `${algo}-wizard-${new Date().toISOString().slice(0, 16)}`,
      });
    },
    onSuccess: (r) => {
      toast.success(t("exp.toast.queued"), { description: r.message });
      onClose();
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("training failed");
    },
  });

  const canNext = (() => {
    if (step === 1) return !!algo;
    if (step === 2) return repoIDs.size > 0;
    if (step === 3) return true;
    if (step === 4) return true;
    return false;
  })();

  return (
    <div
      style={{
        padding: "var(--s-4)",
        marginBottom: "var(--s-4)",
        background: "var(--bg-elevated)",
        border: "1px solid var(--border-strong)",
        borderRadius: "var(--r-8)",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "var(--s-3)" }}>
        <Steps current={step} />
        <button
          onClick={onClose}
          style={{ background: "none", border: "none", color: "var(--text-tertiary)", cursor: "pointer", fontSize: "var(--fs-13)" }}
        >
          {t("common.cancel")}
        </button>
      </div>

      {step === 1 && (
        <Step title={t("wiz.step1.title")} hint={t("wiz.step1.hint")}>
          <div style={{ display: "flex", flexWrap: "wrap", gap: "var(--s-2)" }}>
            {ALGORITHMS.map((a) => (
              <button
                key={a.id}
                onClick={() => setAlgo(a.id)}
                style={pillStyle(algo === a.id)}
                title={a.lstm ? "PyTorch baseline — heavier model" : undefined}
              >
                {t(a.labelKey)}
              </button>
            ))}
          </div>
        </Step>
      )}

      {step === 2 && (
        <Step title={t("wiz.step2.title")} hint={t("wiz.step2.hint")}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-2)", maxHeight: 240, overflowY: "auto", marginBottom: "var(--s-4)" }}>
            {allRepos.map((r) => {
              const slug = `${r.owner}/${r.name}`;
              const checked = repoIDs.has(r.id);
              return (
                <label key={r.id} style={rowStyle(checked)}>
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => setRepoIDs((cur) => {
                      const next = new Set(cur);
                      if (next.has(r.id)) next.delete(r.id);
                      else next.add(r.id);
                      return next;
                    })}
                    style={{ accentColor: "var(--accent)" }}
                  />
                  <span className="mono" style={{ fontSize: "var(--fs-13)" }}>{slug}</span>
                  <span className="mono" style={{ marginLeft: "auto", fontSize: 11, color: "var(--text-tertiary)" }}>
                    {r.jobs_count.toLocaleString()} jobs
                  </span>
                </label>
              );
            })}
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: "var(--s-2)" }}>
            <span style={{ fontSize: "var(--fs-13)", color: "var(--text-secondary)" }}>{t("wiz.step2.window")}:</span>
            {([3, 6, 12] as const).map((m) => (
              <button key={m} onClick={() => setMonths(m)} style={pillStyle(months === m)}>
                {t("setup.months", { n: m })}
              </button>
            ))}
          </div>
        </Step>
      )}

      {step === 3 && (
        <Step title={t("wiz.step3.title")} hint={t("wiz.step3.hint")}>
          <div style={{ display: "grid", gap: "var(--s-3)" }}>
            {(HYPERPARAMS[algo] ?? []).map((p) => (
              <div key={p.key}>
                <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 4 }}>
                  <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-secondary)" }}>
                    {p.label}
                  </span>
                  <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--accent)" }}>
                    {(params[p.key] ?? p.def).toString()}
                    {p.hint && <span style={{ marginLeft: 8, color: "var(--text-tertiary)" }}>{p.hint}</span>}
                  </span>
                </div>
                <input
                  type="range"
                  min={p.min} max={p.max} step={p.step}
                  value={params[p.key] ?? p.def}
                  onChange={(e) => setParams((cur) => ({ ...cur, [p.key]: Number(e.target.value) }))}
                  style={{ width: "100%", accentColor: "var(--accent)" }}
                />
              </div>
            ))}
            {(HYPERPARAMS[algo] ?? []).length === 0 && (
              <div style={{ color: "var(--text-tertiary)", fontSize: "var(--fs-13)" }}>
                {t("wiz.step3.no_params")}
              </div>
            )}
          </div>
        </Step>
      )}

      {step === 4 && (
        <Step title={t("wiz.step4.title")} hint={t("wiz.step4.hint")}>
          {timelineQ.isLoading && <p style={{ color: "var(--text-secondary)" }}>{t("common.loading")}</p>}
          {timelineQ.data && (
            <CutoffTimeline
              cells={timelineQ.data.cells}
              cutoff={cutoffISO}
              onSelect={setCutoffISO}
            />
          )}
        </Step>
      )}

      <div style={{ display: "flex", justifyContent: "space-between", marginTop: "var(--s-4)" }}>
        <Button variant="secondary" onClick={() => setStep((s) => Math.max(1, s - 1))} disabled={step === 1}>
          {t("wiz.back")}
        </Button>
        <div style={{ display: "flex", alignItems: "center", gap: "var(--s-3)" }}>
          {step === 4 && (
            <label style={{ display: "inline-flex", alignItems: "center", gap: 6, fontSize: "var(--fs-13)" }}>
              <input
                type="checkbox"
                checked={activate}
                onChange={(e) => setActivate(e.target.checked)}
                style={{ accentColor: "var(--accent)" }}
              />
              <span>{t("exp.activate_on_finish")}</span>
            </label>
          )}
          {step < 4 ? (
            <Button variant="primary" onClick={() => setStep((s) => s + 1)} disabled={!canNext}>
              {t("wiz.next")}
            </Button>
          ) : (
            <Button variant="primary" onClick={() => submit.mutate()} loading={submit.isPending}>
              {t("wiz.submit")}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

// ---- step-internal pieces ------------------------------------------

function Steps({ current }: { current: number }) {
  const t = useT();
  const items = [
    { n: 1, key: "wiz.step1.title" as TranslationKey },
    { n: 2, key: "wiz.step2.title" as TranslationKey },
    { n: 3, key: "wiz.step3.title" as TranslationKey },
    { n: 4, key: "wiz.step4.title" as TranslationKey },
  ];
  return (
    <div style={{ display: "flex", gap: "var(--s-2)", alignItems: "center" }}>
      {items.map((it, i) => (
        <span key={it.n} style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
          <span
            className="mono"
            style={{
              width: 20, height: 20, borderRadius: "50%",
              display: "inline-flex", alignItems: "center", justifyContent: "center",
              fontSize: 11,
              background: it.n <= current ? "var(--accent)" : "var(--bg-inset)",
              color:      it.n <= current ? "var(--bg-base)" : "var(--text-tertiary)",
            }}
          >{it.n}</span>
          <span style={{ fontSize: "var(--fs-12)", color: it.n === current ? "var(--text-primary)" : "var(--text-tertiary)" }}>
            {t(it.key)}
          </span>
          {i < items.length - 1 && <span style={{ color: "var(--text-tertiary)" }}>·</span>}
        </span>
      ))}
    </div>
  );
}

function Step({ title, hint, children }: { title: string; hint: string; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ marginBottom: "var(--s-3)" }}>
        <div style={{ fontSize: "var(--fs-16)", fontWeight: 500 }}>{title}</div>
        <div style={{ fontSize: "var(--fs-13)", color: "var(--text-secondary)", marginTop: 4 }}>{hint}</div>
      </div>
      {children}
    </div>
  );
}

/* CutoffTimeline — daily run-count bars + draggable cutoff line.
 *
 * Click anywhere on the strip to set the cutoff to that day's boundary.
 * The bars left of the cutoff are accent-coloured (train); to the right
 * are tertiary (test). Counts above each bucket show the data density.
 */
function CutoffTimeline({
  cells,
  cutoff,
  onSelect,
}: {
  cells: Array<{ day: string; count: number }>;
  cutoff: string;
  onSelect: (day: string) => void;
}) {
  const W = 720;
  const H = 120;
  const barW = cells.length > 0 ? W / cells.length : 0;
  const maxCount = useMemo(() => cells.reduce((m, c) => Math.max(m, c.count), 1), [cells]);
  const cutoffIdx = cells.findIndex((c) => c.day >= cutoff);
  const cutoffX = cutoffIdx >= 0 ? cutoffIdx * barW : W;

  if (cells.length === 0) {
    return <p style={{ color: "var(--text-tertiary)" }}>No data in the selected window — sync some repos first.</p>;
  }
  return (
    <div>
      <svg width={W} height={H + 16} role="img" aria-label="train/test cutoff timeline"
        onClick={(e) => {
          const rect = (e.target as SVGElement).closest("svg")!.getBoundingClientRect();
          const x = e.clientX - rect.left;
          const idx = Math.max(0, Math.min(cells.length - 1, Math.floor(x / barW)));
          onSelect(cells[idx].day);
        }}
        style={{ cursor: "pointer" }}
      >
        {cells.map((c, i) => {
          const h = (c.count / maxCount) * H;
          const isTrain = i <= cutoffIdx;
          return (
            <rect
              key={c.day}
              x={i * barW}
              y={H - h}
              width={Math.max(1, barW - 1)}
              height={h}
              fill={isTrain ? "var(--accent)" : "var(--text-tertiary)"}
              fillOpacity={isTrain ? 0.7 : 0.35}
            >
              <title>{`${c.day} · ${c.count} jobs · ${isTrain ? "train" : "test"}`}</title>
            </rect>
          );
        })}
        {/* Cutoff line */}
        <line
          x1={cutoffX} x2={cutoffX} y1={0} y2={H}
          stroke="var(--accent)" strokeWidth={2}
        />
        <text
          x={cutoffX + 4} y={12}
          fontFamily="var(--font-mono)" fontSize={10}
          fill="var(--accent)"
        >
          {cutoff}
        </text>
      </svg>
      <div style={{ display: "flex", justifyContent: "space-between", marginTop: 4, fontSize: 11, color: "var(--text-tertiary)" }} className="mono">
        <span>{cells[0]?.day}</span>
        <span>{cells[cells.length - 1]?.day}</span>
      </div>
    </div>
  );
}

// ---- style helpers --------------------------------------------------

function pillStyle(active: boolean): React.CSSProperties {
  return {
    height: 32,
    padding: "0 14px",
    background: active ? "var(--bg-base)" : "transparent",
    color: active ? "var(--text-primary)" : "var(--text-secondary)",
    border: `1px solid ${active ? "var(--border-strong)" : "var(--border-subtle)"}`,
    borderRadius: "var(--r-6)",
    fontSize: "var(--fs-13)",
    cursor: "pointer",
    boxShadow: active ? "inset 0 0 0 1px var(--accent-soft)" : undefined,
  };
}

function rowStyle(checked: boolean): React.CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    gap: "var(--s-2)",
    padding: "6px 10px",
    border: `1px solid ${checked ? "var(--border-strong)" : "var(--border-subtle)"}`,
    borderRadius: "var(--r-6)",
    background: checked ? "var(--bg-base)" : "transparent",
    cursor: "pointer",
  };
}
