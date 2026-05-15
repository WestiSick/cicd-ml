import { api } from "./client";
import type { ModelRow } from "./models";

export type FeatureImportance = {
  name: string;
  value: number;
};

export type PredictedActualPoint = {
  job_id: number;
  repo: string;
  job_name: string;
  actual_sec: number;
  predicted_sec: number;
};

export async function getModelDetail(id: number): Promise<ModelRow> {
  return api<ModelRow>(`/api/models/${id}`);
}

export async function getFeatureImportance(id: number, top = 20): Promise<FeatureImportance[]> {
  const r = await api<{ features: FeatureImportance[] }>(`/api/models/${id}/feature-importance?top=${top}`);
  return r.features;
}

export async function getPredictedVsActual(id: number, limit = 1000): Promise<PredictedActualPoint[]> {
  const r = await api<{ points: PredictedActualPoint[] }>(`/api/models/${id}/predicted-vs-actual?limit=${limit}`);
  return r.points;
}
