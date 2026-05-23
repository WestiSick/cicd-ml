import { api } from "./client";

/* /api/dashboard/load-24h — hourly counts for the dashboard sparkline. */
export type LoadBucket = {
  hour: string;     // ISO timestamp of the hour-start (UTC)
  jobs: number;
  mean_sec: number;
};

export async function fetchLoad24h(): Promise<LoadBucket[]> {
  const r = await api<{ buckets: LoadBucket[] }>("/api/dashboard/load-24h");
  return r.buckets;
}
