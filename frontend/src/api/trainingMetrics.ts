import { api } from "./client";

export type IterationMetric = {
  training_job_id: number;
  iteration: number;
  train_loss?: number;
  val_mae?: number;
  val_rmse?: number;
  val_mape?: number;
  ts: string;
};

export async function listTrainingMetrics(trainingJobId: number): Promise<IterationMetric[]> {
  const r = await api<{ metrics: IterationMetric[] }>(`/api/training/${trainingJobId}/metrics`);
  return r.metrics;
}
