import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { toast } from "sonner";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { Button } from "@/components/Button";
import { StatusChip } from "@/components/StatusChip";
import { EmptyState } from "@/components/EmptyState";
import { ApiError } from "@/api/client";
import { activateModel, listModels, startTraining, type ModelRow } from "@/api/models";
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
  const modelsQ = useQuery({ queryKey: ["models"], queryFn: listModels, refetchInterval: 5_000 });
  const trainingsQ = useQuery({
    queryKey: ["bg-jobs", "train_model"],
    queryFn: () => listBGJobs({ limit: 20 }),
    refetchInterval: 3_000,
  });

  const [algo, setAlgo] = useState("xgboost");
  const [activate, setActivate] = useState(true);
  const [optunaTrials, setOptunaTrials] = useState(0); // 0 = off

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
            <Button variant="ghost" onClick={() => exportPack.mutate()} loading={exportPack.isPending}>
              {t("exp.export_pack")}
            </Button>
            <Button variant="primary" onClick={() => train.mutate()} loading={train.isPending}>
              {t("exp.train", { algo })}
            </Button>
          </>
        }
      />

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
                <Th>{t("exp.col.id")}</Th>
                <Th>{t("exp.col.algo")}</Th>
                <Th>{t("exp.col.name")}</Th>
                <Th>MAE test (s)</Th>
                <Th>RMSE test (s)</Th>
                <Th>MAPE</Th>
                <Th>R²</Th>
                <Th>Spearman</Th>
                <Th>{t("exp.col.trained")}</Th>
                <Th>{" "}</Th>
              </tr>
            </thead>
            <tbody>
              {modelsQ.data.map((m) => <ModelRowEl key={m.id} m={m} onActivate={() => activate1.mutate(m.id)} />)}
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

function ModelRowEl({ m, onActivate }: { m: ModelRow; onActivate: () => void }) {
  const t = useT();
  const metric = (k: string) => {
    const v = m.metrics?.[k];
    if (typeof v !== "number" || !isFinite(v)) return "—";
    return v.toFixed(k.includes("mape") || k.includes("r2") || k.includes("spearman") ? 3 : 1);
  };
  return (
    <tr style={{ borderTop: "1px solid var(--border-subtle)" }}>
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
        {m.is_active ? (
          <StatusChip status="synced" />
        ) : (
          <Button size="sm" variant="ghost" onClick={onActivate}>{t("common.activate")}</Button>
        )}
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
