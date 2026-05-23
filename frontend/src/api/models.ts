import { api } from "./client";

export type ModelRow = {
  id: number;
  name: string;
  algo: string;
  params: Record<string, unknown>;
  metrics: Record<string, number>;
  artifact_path?: string;
  training_job_id?: number;
  feature_version: number;
  is_active: boolean;
  trained_at: string;
};

export type StartTrainingRequest = {
  algo: string;
  params?: Record<string, unknown>;
  repo_ids?: number[];
  /** ISO-8601 timestamp; rows older than this are excluded from the
   *  training dataset (passed to ml-service as the `since` field on
   *  load_jobs_df). Used by the wizard to enforce a time-based cutoff. */
  since?: string;
  activate?: boolean;
  name?: string;
  /** When >= 2, ml-service runs an Optuna hyperparameter search of
   *  `optuna_trials` trials and persists the best-found model. */
  optuna_trials?: number;
  /** Tier-2 continual learning. When true, ml-service up-weights
   *  training rows the previous model got wrong via sample_weight.
   *  Pairs with the webhook-time per-(repo, workflow) EMA calibration
   *  surfaced in /admin → Calibrations. */
  error_weighted?: boolean;
  /** Strength of the up-weighting. Defaults to 1.0 server-side.
   *  Useful values 0.5–2.0; larger amplifies outliers harder. */
  error_weight_alpha?: number;
};

export type StartTrainingResponse = {
  bg_job_id: number;
  message: string;
};

export async function listModels(): Promise<ModelRow[]> {
  const r = await api<{ models: ModelRow[] }>("/api/models");
  return r.models;
}

export async function activateModel(id: number): Promise<void> {
  await api(`/api/models/${id}/activate`, { method: "POST" });
}

export async function startTraining(req: StartTrainingRequest): Promise<StartTrainingResponse> {
  return await api<StartTrainingResponse>("/api/training", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export type CVRequest = {
  algo: string;
  params?: Record<string, unknown>;
  repo_ids?: number[];
  n_splits?: number;
};

export type CVResponse = {
  algo: string;
  n_splits: number;
  fold_metrics: Array<Record<string, number>>;
  mean_metrics: Record<string, number>;
  std_metrics:  Record<string, number>;
  total_train_size: number;
  total_test_size:  number;
};

export async function crossValidate(req: CVRequest): Promise<CVResponse> {
  return api<CVResponse>("/api/training/cv", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function deleteModel(id: number): Promise<{ predictions_deleted: number }> {
  return api(`/api/models/${id}`, { method: "DELETE" });
}

// Browser-friendly download URL. We don't fetch the bytes through the
// `api()` wrapper because the response is a binary stream; let the browser
// follow the URL and save the joblib file directly.
export function modelDownloadURL(id: number): string {
  const base = (import.meta.env.VITE_API_BASE as string) || "http://localhost:8080";
  return `${base}/api/models/${id}/download`;
}
