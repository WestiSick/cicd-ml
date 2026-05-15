import { api } from "./client";

export type ThesisExportFile = {
  name: string;
  rows: number;
};

export type ThesisExportResponse = {
  directory: string;
  timestamp: string;
  files: ThesisExportFile[];
};

export async function exportThesisPack(): Promise<ThesisExportResponse> {
  return api<ThesisExportResponse>("/api/experiments/export-thesis-pack", { method: "POST" });
}
