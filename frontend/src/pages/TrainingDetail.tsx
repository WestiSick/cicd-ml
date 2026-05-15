import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { StatusChip } from "@/components/StatusChip";
import { EmptyState } from "@/components/EmptyState";
import { LineChart, type Series } from "@/components/LineChart";
import { ScatterPlot } from "@/components/ScatterPlot";
import { HBarChart } from "@/components/HBarChart";
import { getBGJob, type BGJob } from "@/api/bgjobs";
import { listTrainingMetrics, type IterationMetric } from "@/api/trainingMetrics";
import { listModels } from "@/api/models";
import { getFeatureImportance, getPredictedVsActual } from "@/api/modelDetail";
import { useWebSocket } from "@/hooks/useWebSocket";

/* /experiments/jobs/:id — live training run view.
 *
 * Three blocks (per plan §7.4):
 *   1. Status row: chip + progress bar + final message.
 *   2. Per-iteration line chart (train loss + val rmse).
 *   3. Tail of log messages (we use bg_jobs.message as a poor man's
 *      log tail — fine for the current granularity; bumps to logs_tail
 *      column later if we capture actual stderr.)
 *
 * The WebSocket /ws/training/:id sends a "metric" event on each
 * iteration. We append into local state for O(1) updates. On reload
 * we seed from REST so progress mid-flight is preserved across
 * refreshes.
 */
export function TrainingDetail() {
  const { id } = useParams();
  const jobId = Number(id);

  const jobQ = useQuery({
    queryKey: ["bg-job", jobId],
    queryFn: () => getBGJob(jobId),
    refetchInterval: (q) => {
      const data = q.state.data as BGJob | undefined;
      return data && (data.status === "queued" || data.status === "running") ? 1500 : false;
    },
    enabled: !Number.isNaN(jobId),
  });

  const metricsQ = useQuery({
    queryKey: ["training-metrics", jobId],
    queryFn: () => listTrainingMetrics(jobId),
    enabled: !Number.isNaN(jobId),
  });

  // The model produced by this training run — needed for the scatter
  // plot and feature importance, which key by model_id (not bg_job id).
  // We list models and match by training_job_id rather than adding a
  // dedicated index endpoint — the list is tiny.
  const modelsQ = useQuery({
    queryKey: ["models", "for-training", jobId],
    queryFn: listModels,
    enabled: !Number.isNaN(jobId),
  });
  const matchedModel = useMemo(
    () => (modelsQ.data ?? []).find((m) => m.training_job_id === jobId),
    [modelsQ.data, jobId],
  );

  const importanceQ = useQuery({
    queryKey: ["feature-importance", matchedModel?.id],
    queryFn: () => getFeatureImportance(matchedModel!.id, 20),
    enabled: !!matchedModel,
  });

  const pvaQ = useQuery({
    queryKey: ["predicted-actual", matchedModel?.id],
    queryFn: () => getPredictedVsActual(matchedModel!.id, 1000),
    enabled: !!matchedModel,
  });

  // Live stream — append new iteration rows as they arrive.
  const [liveMetrics, setLiveMetrics] = useState<IterationMetric[]>([]);
  useEffect(() => {
    if (metricsQ.data) setLiveMetrics(metricsQ.data);
  }, [metricsQ.data]);

  useWebSocket(`/ws/training/${jobId}`, (msg) => {
    if (msg.type !== "metric") return;
    const m = msg.data as IterationMetric;
    setLiveMetrics((cur) => {
      // Replace if iteration already exists (idempotent on reconnects).
      const next = cur.filter((x) => x.iteration !== m.iteration);
      next.push(m);
      next.sort((a, b) => a.iteration - b.iteration);
      return next;
    });
  });

  const charts: { trainLoss: Series; valRMSE: Series; valMAE: Series } = useMemo(() => {
    const trainLoss: Series = {
      label: "train_loss",
      color: "var(--info)",
      points: liveMetrics
        .filter((m) => m.train_loss !== undefined && m.train_loss !== null)
        .map((m) => ({ x: m.iteration, y: m.train_loss as number })),
    };
    const valRMSE: Series = {
      label: "val_rmse",
      color: "var(--accent)",
      points: liveMetrics
        .filter((m) => m.val_rmse !== undefined && m.val_rmse !== null)
        .map((m) => ({ x: m.iteration, y: m.val_rmse as number })),
    };
    const valMAE: Series = {
      label: "val_mae",
      color: "var(--ok)",
      points: liveMetrics
        .filter((m) => m.val_mae !== undefined && m.val_mae !== null)
        .map((m) => ({ x: m.iteration, y: m.val_mae as number })),
    };
    return { trainLoss, valRMSE, valMAE };
  }, [liveMetrics]);

  const job = jobQ.data;
  const pct = job && job.total > 0 ? Math.round((job.progress / job.total) * 100) : 0;

  return (
    <>
      <PageHeader
        title={`Training #${id}`}
        subtitle={job ? `${(job.payload as Record<string, unknown>)?.algo ?? "unknown"} — ${job.status}` : "Loading…"}
      />

      {jobQ.isLoading && <p style={mutedText}>Loading…</p>}

      {job && (
        <Card style={{ marginBottom: "var(--s-4)" }}>
          <div style={{ display: "flex", alignItems: "center", gap: "var(--s-3)" }}>
            <StatusChip status={job.status} />
            <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-tertiary)" }}>
              {job.started_at ? new Date(job.started_at).toISOString().slice(11, 19) : "—"}
              {job.finished_at && " → " + new Date(job.finished_at).toISOString().slice(11, 19)}
            </span>
            <span style={{ flex: 1 }} />
            <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-tertiary)" }}>
              {job.progress}/{job.total || "?"} ({pct}%)
            </span>
          </div>
          <div style={{
            marginTop: "var(--s-2)",
            height: 3,
            background: "var(--bg-inset)",
            borderRadius: "var(--r-pill)",
            overflow: "hidden",
          }}>
            <div
              style={{
                height: "100%",
                width: `${pct}%`,
                background: job.status === "failed" ? "var(--err)" : "var(--accent)",
                transition: "width var(--t-entry) var(--ease)",
              }}
            />
          </div>
          {job.message && (
            <p style={{ marginTop: "var(--s-3)", color: "var(--text-secondary)", fontSize: "var(--fs-13)" }}>
              {job.message}
            </p>
          )}
          {job.error && (
            <p style={{
              marginTop: "var(--s-2)",
              padding: "6px 10px",
              border: "1px solid var(--err-soft)",
              borderRadius: "var(--r-6)",
              color: "var(--err)",
              fontSize: "var(--fs-13)",
              fontFamily: "var(--font-mono)",
            }}>
              {job.error}
            </p>
          )}
        </Card>
      )}

      <h2 style={sectionTitleStyle}>Per-iteration metrics</h2>
      {liveMetrics.length === 0 ? (
        <EmptyState
          title="No iteration metrics streamed yet."
          hint="Boosted models (XGBoost / LightGBM) emit one point per iteration. Linear / Random Forest emit a single terminal point on completion."
        />
      ) : (
        <Card>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-4)" }}>
            <ChartBlock title="Train loss" series={charts.trainLoss} />
            <ChartBlock title="Validation RMSE" series={charts.valRMSE} />
          </div>
          {charts.valMAE.points.length > 0 && (
            <div style={{ marginTop: "var(--s-4)" }}>
              <ChartBlock title="Validation MAE" series={charts.valMAE} fullWidth />
            </div>
          )}
        </Card>
      )}

      {/* Predicted vs actual scatter — visible once predictions have
          been written for the model produced by this run. */}
      {matchedModel && (
        <>
          <h2 style={{ ...sectionTitleStyle, marginTop: "var(--s-8)" }}>
            Predicted vs actual
            <span className="mono" style={{ color: "var(--text-tertiary)", marginLeft: 8, fontSize: "var(--fs-12)", fontWeight: 400 }}>
              model #{matchedModel.id} · {matchedModel.algo}
            </span>
          </h2>
          {pvaQ.isLoading && <p style={mutedText}>Loading…</p>}
          {pvaQ.data && pvaQ.data.length === 0 && (
            <EmptyState
              title="No predictions persisted yet."
              hint="Predictions land in the database for the test slice after each training. If you trained before model_id was wired, retrain."
            />
          )}
          {pvaQ.data && pvaQ.data.length > 0 && (
            <Card>
              <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}>
                {pvaQ.data.length} job(s) · dashed line = perfect prediction
              </div>
              <ScatterPlot
                points={pvaQ.data.map((p) => ({ x: p.actual_sec, y: p.predicted_sec }))}
                width={560}
                height={400}
              />
            </Card>
          )}

          <h2 style={{ ...sectionTitleStyle, marginTop: "var(--s-8)" }}>
            Top features
          </h2>
          {importanceQ.isLoading && <p style={mutedText}>Loading…</p>}
          {importanceQ.data && importanceQ.data.length === 0 && (
            <EmptyState
              title="No feature importance recorded."
              hint="Linear models report absolute coefficient magnitudes; tree models report Gini. If empty here, the model was trained before this column existed — retrain."
            />
          )}
          {importanceQ.data && importanceQ.data.length > 0 && (
            <Card>
              <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}>
                top {importanceQ.data.length} by importance value
              </div>
              <HBarChart items={importanceQ.data} width={720} />
            </Card>
          )}
        </>
      )}
    </>
  );
}

function ChartBlock({ title, series, fullWidth }: { title: string; series: Series; fullWidth?: boolean }) {
  return (
    <div>
      <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}>
        {title}
      </div>
      <LineChart series={[series]} xLabel="iteration" width={fullWidth ? 720 : 340} />
    </div>
  );
}

const sectionTitleStyle: React.CSSProperties = { fontSize: "var(--fs-16)", fontWeight: 500, margin: "0 0 var(--s-3)" };
const mutedText: React.CSSProperties = { color: "var(--text-secondary)", fontSize: "var(--fs-13)", margin: 0 };
