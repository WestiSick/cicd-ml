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
  activate?: boolean;
  name?: string;
  /** When >= 2, ml-service runs an Optuna hyperparameter search of
   *  `optuna_trials` trials and persists the best-found model. */
  optuna_trials?: number;
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
