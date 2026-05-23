import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { Button } from "@/components/Button";
import { StatusChip } from "@/components/StatusChip";
import { EmptyState } from "@/components/EmptyState";
import { ApiError } from "@/api/client";
import { activateModel, crossValidate, deleteModel, listModels, modelDownloadURL, startTraining, type CVResponse, type ModelRow } from "@/api/models";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { TrainWizard } from "@/components/TrainWizard";
import { listBGJobs, type BGJob } from "@/api/bgjobs";
import { exportThesisPack } from "@/api/thesisExport";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useT } from "@/i18n";
import type { TranslationKey } from "@/i18n/types";

const ALGORITHMS: { id: string; labelKey: TranslationKey }[] = [
  { id: "linear",   labelKey: "setup.algo.linear" },
  { id: "rf",       labelKey: "setup.algo.rf" },
  { id: "xgboost",  labelKey: "setup.algo.xgboost" },
  { id: "lightgbm", labelKey: "setup.algo.lightgbm" },
  { id: "mlp",      labelKey: "setup.algo.mlp" },
];

/* /experiments — train new models, view metrics, activate.
 *
 * Three sections, top to bottom:
 *   1. Trained models table — id, algo, MAE/RMSE/MAPE/R²/Spearman, active flag.
 *   2. "Train new model" form (algo + activate-on-finish checkbox).
 *   3. Recent training bg_jobs (live, streamed via /ws/bg-jobs).
 *
 * The wizard from plan §7-4 (params + Optuna search + dataset filter +
 * time-cutoff visualisation) is intentionally deferred — Linear/RF/
 * XGBoost on defaults gives the dissertation a meaningful comparison
 * already, and adding knobs without a needs-driven story would clutter
 * the UI. Easy to extend later: just expand the form below.
 */
export function Experiments() {
  const t = useT();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const modelsQ = useQuery({ queryKey: ["models"], queryFn: listModels, refetchInterval: 5_000 });
  // Selection set drives the comparison action. Capped at 5 in the UI
  // because the ModelCompare page slices to 5 anyway — better to surface
  // the limit at click time than silently truncate.
  const [comparison, setComparison] = useState<Set<number>>(new Set());

  function toggleCompare(id: number) {
    setComparison((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else {
        if (next.size >= 5) {
          toast.error(t("exp.compare.too_many"));
          return cur;
        }
        next.add(id);
      }
      return next;
    });
  }

  function openComparison() {
    if (comparison.size < 2) {
      toast.error(t("exp.compare.select"));
      return;
    }
    const ids = Array.from(comparison).join(",");
    navigate(`/experiments/compare?ids=${ids}`);
  }
  const trainingsQ = useQuery({
    queryKey: ["bg-jobs", "train_model"],
    queryFn: () => listBGJobs({ limit: 20 }),
    refetchInterval: 3_000,
  });

  const [algo, setAlgo] = useState("xgboost");
  const [activate, setActivate] = useState(true);
  const [optunaTrials, setOptunaTrials] = useState(0); // 0 = off
  const [cvSplits, setCvSplits] = useState(5);
  const [cvResult, setCvResult] = useState<CVResponse | null>(null);
  const [showWizard, setShowWizard] = useState(false);

  // Live WS push — refresh queries whenever bg_jobs broadcast a change.
  useWebSocket("/ws/bg-jobs", () => {
    qc.invalidateQueries({ queryKey: ["bg-jobs", "train_model"] });
    qc.invalidateQueries({ queryKey: ["models"] });
  });

  const train = useMutation({
    mutationFn: () => startTraining({
      algo,
      activate,
      name: `${algo}${optunaTrials >= 2 ? `-optuna${optunaTrials}` : ""}-${new Date().toISOString().slice(0, 16)}`,
      optuna_trials: optunaTrials >= 2 ? optunaTrials : undefined,
    }),
    onSuccess: (r) => {
      toast.success(t("exp.toast.queued"), { description: r.message });
      qc.invalidateQueries({ queryKey: ["bg-jobs", "train_model"] });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("training failed");
    },
  });

  const activate1 = useMutation({
    mutationFn: (id: number) => activateModel(id),
    onSuccess: (_, id) => {
      toast.success(t("exp.toast.activated", { id }));
      qc.invalidateQueries({ queryKey: ["models"] });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("activate failed");
    },
  });

  // Filter bg_jobs to only training-related ones for the live panel below.
  const recentTrainings = useMemo(
    () => (trainingsQ.data ?? []).filter((j) => j.kind === "train_model"),
    [trainingsQ.data]
  );

  const cv = useMutation({
    mutationFn: async () => {
      const started = Date.now();
      const r = await crossValidate({ algo, n_splits: cvSplits });
      return { r, elapsedSec: Math.round((Date.now() - started) / 1000) };
    },
    onSuccess: ({ r, elapsedSec }) => {
      setCvResult(r);
      toast.success(t("exp.cv.toast_done", { n: r.n_splits, sec: elapsedSec }));
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("CV failed");
    },
  });

  const exportPack = useMutation({
    mutationFn: exportThesisPack,
    onSuccess: (r) => {
      toast.success(t("exp.export.toast"), {
        description: `${r.files.length} CSV @ ${r.directory}`,
      });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("export failed");
    },
  });

  return (
    <>
      <PageHeader
        title={t("exp.title")}
        subtitle={t("exp.subtitle")}
        actions={
          <>
            {comparison.size > 0 && (
              <Button variant="ghost" onClick={openComparison}>
                {t("exp.compare_button", { n: comparison.size })}
              </Button>
            )}
            <Button variant="ghost" onClick={() => exportPack.mutate()} loading={exportPack.isPending}>
              {t("exp.export_pack")}
            </Button>
            <Button variant="ghost" onClick={() => setShowWizard((v) => !v)}>
              {t("exp.wizard_button")}
            </Button>
            <Button variant="primary" onClick={() => train.mutate()} loading={train.isPending}>
              {t("exp.train", { algo })}
            </Button>
          </>
        }
      />

      {showWizard && <TrainWizard onClose={() => setShowWizard(false)} />}

      <Card style={{ marginBottom: "var(--s-4)" }}>
        <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}>
          {t("exp.new_run")}
        </div>
        <div style={{ display: "flex", flexWrap: "wrap", gap: "var(--s-2)", alignItems: "center" }}>
          {ALGORITHMS.map((a) => (
            <button
              key={a.id}
              onClick={() => setAlgo(a.id)}
              style={pillStyle(algo === a.id)}
            >
              {t(a.labelKey)}
            </button>
          ))}
          <label style={{ display: "inline-flex", alignItems: "center", gap: 6, fontSize: "var(--fs-13)", marginLeft: "var(--s-3)" }}>
            <input
              type="checkbox"
              checked={activate}
              onChange={(e) => setActivate(e.target.checked)}
              style={{ accentColor: "var(--accent)" }}
            />
            <span>{t("exp.activate_on_finish")}</span>
          </label>
        </div>

        {/* Walk-forward cross-validation — synchronous, no model persisted */}
        <div style={{ marginTop: "var(--s-4)", paddingTop: "var(--s-3)", borderTop: "1px solid var(--border-subtle)" }}>
          <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}>
            {t("exp.cv.label")}
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: "var(--s-2)", alignItems: "center" }}>
            {[3, 5, 8].map((n) => (
              <button key={n} onClick={() => setCvSplits(n)} style={pillStyle(cvSplits === n)}>
                {t("exp.cv.splits", { n })}
              </button>
            ))}
            <Button variant="ghost" onClick={() => cv.mutate()} loading={cv.isPending}>
              {cv.isPending ? t("exp.cv.running") : t("exp.cv.button")}
            </Button>
            <span style={{ marginLeft: "var(--s-3)", color: "var(--text-tertiary)", fontSize: "var(--fs-12)" }}>
              {t("exp.cv.hint")}
            </span>
          </div>
          {cvResult && (
            <div style={{ marginTop: "var(--s-3)" }}>
              <CVResultTable cv={cvResult} />
            </div>
          )}
        </div>

        {/* Optuna hyperparameter search */}
        <div style={{ marginTop: "var(--s-4)", paddingTop: "var(--s-3)", borderTop: "1px solid var(--border-subtle)" }}>
          <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}>
            {t("exp.optuna.label")}
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: "var(--s-2)", alignItems: "center" }}>
            {[0, 10, 30, 50, 100].map((n) => (
              <button key={n} onClick={() => setOptunaTrials(n)} style={pillStyle(optunaTrials === n)}>
                {n === 0 ? t("exp.optuna.off") : t("exp.optuna.trials", { n })}
              </button>
            ))}
            <span style={{ marginLeft: "var(--s-3)", color: "var(--text-tertiary)", fontSize: "var(--fs-12)" }}>
              {optunaTrials >= 2
                ? t("exp.optuna.hint_on", { sec: Math.max(1, Math.round(optunaTrials * 0.15)) })
                : t("exp.optuna.hint_off")}
            </span>
          </div>
        </div>
      </Card>

      <h2 style={sectionTitleStyle}>{t("exp.trained_models")}</h2>
      {modelsQ.isLoading && <p style={mutedText}>{t("common.loading")}</p>}
      {modelsQ.data && modelsQ.data.length === 0 && (
        <EmptyState
          title={t("exp.empty.title")}
          hint={t("exp.empty.hint")}
          action={<Button variant="primary" onClick={() => train.mutate()} loading={train.isPending}>{t("exp.empty.action")}</Button>}
        />
      )}
      {modelsQ.data && modelsQ.data.length > 0 && (
        <Card>
          <table style={tableStyle}>
            <thead>
              <tr>
                <Th>{" "}</Th>
                <Th>{t("exp.col.id")}</Th>
                <Th>{t("exp.col.algo")}</Th>
                <Th>{t("exp.col.name")}</Th>
                <Th>MAE test (s)</Th>
                <Th>RMSE test (s)</Th>
                <Th>MAPE</Th>
                <Th>R²</Th>
                <Th>Spearman</Th>
                <Th>{t("exp.col.trained")}</Th>
                <Th>{t("exp.col.actions")}</Th>
              </tr>
            </thead>
            <tbody>
              {modelsQ.data.map((m) => (
                <ModelRowEl
                  key={m.id}
                  m={m}
                  selected={comparison.has(m.id)}
                  onToggleSelect={() => toggleCompare(m.id)}
                  onActivate={() => activate1.mutate(m.id)}
                  onDeleted={() => qc.invalidateQueries({ queryKey: ["models"] })}
                />
              ))}
            </tbody>
          </table>
        </Card>
      )}

      <h2 style={{ ...sectionTitleStyle, marginTop: "var(--s-8)" }}>{t("exp.recent_runs")}</h2>
      {recentTrainings.length === 0 ? (
        <EmptyState title={t("exp.recent_runs")} hint={t("exp.empty.hint")} />
      ) : (
        <Card>
          <div style={{ display: "grid", gap: 0 }}>
            {recentTrainings.map((j) => <TrainingRow key={j.id} job={j} />)}
          </div>
        </Card>
      )}
    </>
  );
}

function ModelRowEl({
  m,
  selected,
  onToggleSelect,
  onActivate,
  onDeleted,
}: {
  m: ModelRow;
  selected: boolean;
  onToggleSelect: () => void;
  onActivate: () => void;
  onDeleted: () => void;
}) {
  const t = useT();
  const [confirmDelete, setConfirmDelete] = useState(false);

  const remove = useMutation({
    mutationFn: () => deleteModel(m.id),
    onSuccess: () => {
      toast.success(t("exp.toast.deleted"));
      onDeleted();
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError && err.code === "model_is_active") {
        toast.error(t("exp.toast.cannot_delete_active"));
      } else if (err instanceof ApiError) {
        toast.error(err.message, { description: err.userAction });
      } else {
        toast.error("delete failed");
      }
    },
  });

  const metric = (k: string) => {
    const v = m.metrics?.[k];
    if (typeof v !== "number" || !isFinite(v)) return "—";
    return v.toFixed(k.includes("mape") || k.includes("r2") || k.includes("spearman") ? 3 : 1);
  };
  return (
    <tr style={{ borderTop: "1px solid var(--border-subtle)" }}>
      <Td>
        <input
          type="checkbox"
          checked={selected}
          onChange={onToggleSelect}
          style={{ accentColor: "var(--accent)", cursor: "pointer" }}
          title="add to comparison"
        />
      </Td>
      <Td mono>
        {m.training_job_id ? (
          <Link
            to={`/experiments/jobs/${m.training_job_id}`}
            style={{ color: "var(--text-primary)", borderBottom: "1px dotted var(--border-strong)" }}
          >
            {m.id}
          </Link>
        ) : (
          m.id
        )}
      </Td>
      <Td mono>{m.algo}</Td>
      <Td mono small>{m.name}</Td>
      <Td mono>{metric("mae_test_sec")}</Td>
      <Td mono>{metric("rmse_test_sec")}</Td>
      <Td mono>{metric("mape_test")}</Td>
      <Td mono>{metric("r2_test")}</Td>
      <Td mono>{metric("spearman_test")}</Td>
      <Td mono small>{new Date(m.trained_at).toISOString().slice(0, 16).replace("T", " ")}</Td>
      <Td>
        <div style={{ display: "flex", gap: "var(--s-2)", alignItems: "center", justifyContent: "flex-end" }}>
          {m.is_active ? (
            <StatusChip status="synced" />
          ) : (
            <Button size="sm" variant="ghost" onClick={onActivate}>{t("common.activate")}</Button>
          )}
          <a
            href={modelDownloadURL(m.id)}
            target="_blank"
            rel="noopener noreferrer"
            style={{ fontSize: 12, color: "var(--text-secondary)", textDecoration: "none" }}
            title={t("exp.action.download")}
          >
            {t("common.download")}
          </a>
          {!m.is_active && (
            <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(true)}>
              {t("exp.action.delete")}
            </Button>
          )}
        </div>
        <ConfirmDialog
          open={confirmDelete}
          title={t("exp.delete.title")}
          message={t("exp.delete.message")}
          confirmLabel={t("exp.delete.confirm")}
          requireText={m.name}
          danger
          onCancel={() => setConfirmDelete(false)}
          onConfirm={() => { setConfirmDelete(false); remove.mutate(); }}
        />
      </Td>
    </tr>
  );
}

function TrainingRow({ job }: { job: BGJob }) {
  const pct = job.total > 0 ? Math.round((job.progress / job.total) * 100) : 0;
  return (
    <Link
      to={`/experiments/jobs/${job.id}`}
      style={{
        display: "grid",
        gridTemplateColumns: "100px 1fr 80px",
        alignItems: "center",
        gap: "var(--s-3)",
        padding: "var(--s-2) 0",
        borderTop: "1px solid var(--border-subtle)",
        color: "inherit",
      }}
    >
      <div style={{ display: "flex", gap: "var(--s-2)", alignItems: "center" }}>
        <StatusChip status={job.status} />
      </div>
      <div>
        <div style={{ fontSize: "var(--fs-13)" }}>
          {job.message ?? (job.error ? `error: ${job.error}` : `algo: ${(job.payload as Record<string, unknown>)?.algo ?? "?"}`)}
        </div>
        <div style={{ marginTop: 4, height: 3, background: "var(--bg-inset)", borderRadius: "var(--r-pill)", overflow: "hidden" }}>
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
      <div className="mono" style={{ textAlign: "right", fontSize: "var(--fs-12)", color: "var(--text-tertiary)" }}>
        {job.progress}/{job.total || "—"}
      </div>
    </Link>
  );
}

const sectionTitleStyle: React.CSSProperties = { fontSize: "var(--fs-16)", fontWeight: 500, margin: "0 0 var(--s-3)" };
const mutedText: React.CSSProperties = { color: "var(--text-secondary)", fontSize: "var(--fs-13)", margin: 0 };
const tableStyle: React.CSSProperties = { width: "100%", borderCollapse: "collapse", fontSize: "var(--fs-13)", marginTop: "var(--s-3)" };

function pillStyle(active: boolean): React.CSSProperties {
  return {
    height: 30,
    padding: "0 12px",
    background: active ? "var(--bg-base)" : "transparent",
    color: active ? "var(--text-primary)" : "var(--text-secondary)",
    border: `1px solid ${active ? "var(--border-strong)" : "var(--border-subtle)"}`,
    borderRadius: "var(--r-6)",
    fontSize: "var(--fs-13)",
    cursor: "pointer",
    boxShadow: active ? "inset 0 0 0 1px var(--accent-soft)" : undefined,
  };
}

function Th({ children }: { children: React.ReactNode }) {
  return (
    <th className="caps" style={{ textAlign: "left", padding: "var(--s-2) var(--s-1)", color: "var(--text-tertiary)", fontWeight: 500 }}>
      {children}
    </th>
  );
}

function Td({ children, mono, small }: { children: React.ReactNode; mono?: boolean; small?: boolean }) {
  return (
    <td className={mono ? "mono" : undefined} style={{ padding: "var(--s-2) var(--s-1)", fontSize: small ? "var(--fs-12)" : undefined }}>
      {children}
    </td>
  );
}

/* CVResultTable — renders the walk-forward CV summary.
 *
 * Layout: one column per fold (limited to first 8 to keep horizontal scroll
 * tame) plus mean and std columns. Rows are the canonical metrics in the
 * same order as the comparison page so the user's eye doesn't have to
 * re-adapt when flipping between pages. */
function CVResultTable({ cv }: { cv: CVResponse }) {
  const t = useT();
  const keys = [
    { k: "mae_test_sec",  label: "MAE (s)", digits: 1 },
    { k: "rmse_test_sec", label: "RMSE (s)", digits: 1 },
    { k: "mape_test",     label: "MAPE", digits: 3 },
    { k: "r2_test",       label: "R²", digits: 3 },
    { k: "spearman_test", label: "Spearman", digits: 3 },
    { k: "ndcg_at_10",    label: "NDCG@10", digits: 3 },
  ];
  const fmt = (v: number | undefined, d: number) =>
    typeof v === "number" && isFinite(v) ? v.toFixed(d) : "—";

  return (
    <div>
      <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)", fontSize: 11 }}>
        {t("exp.cv.title", { algo: cv.algo, n: cv.n_splits })}
      </div>
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: "var(--fs-12)" }}>
        <thead>
          <tr>
            <Th>metric</Th>
            {cv.fold_metrics.slice(0, 8).map((_, i) => (
              <Th key={i}>{t("exp.cv.fold")} {i + 1}</Th>
            ))}
            <Th>{t("exp.cv.mean")}</Th>
            <Th>±{t("exp.cv.std")}</Th>
          </tr>
        </thead>
        <tbody>
          {keys.map((row) => (
            <tr key={row.k} style={{ borderTop: "1px solid var(--border-subtle)" }}>
              <Td mono>{row.label}</Td>
              {cv.fold_metrics.slice(0, 8).map((f, i) => (
                <Td key={i} mono small>{fmt(f[row.k], row.digits)}</Td>
              ))}
              <Td mono><strong>{fmt(cv.mean_metrics[row.k], row.digits)}</strong></Td>
              <Td mono small>{fmt(cv.std_metrics[row.k], row.digits)}</Td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
