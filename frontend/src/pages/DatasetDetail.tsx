import { useState } from "react";
import { useParams, Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { EmptyState } from "@/components/EmptyState";
import { BarChart } from "@/components/BarChart";
import { StatusChip } from "@/components/StatusChip";
import { PushHeatmap } from "@/components/PushHeatmap";
import {
  datasetExportCSVURL,
  fetchDatasetDetail,
  fetchFeaturePreview,
  fetchPushRecommendations,
} from "@/api/repos";
import { useT } from "@/i18n";
import { formatDuration } from "@/lib/format";

/* /datasets/:id — per-repo statistics page.
 *
 * Backed by GET /api/datasets/{id}. Renders five blocks:
 *   - Duration histogram (log-binned, fixed boundaries — Chapter 3 chart)
 *   - Top workflows by run count, with p50/p95
 *   - Top job names, with mean/p50
 *   - Branch breakdown
 *   - Job conclusions (success/failure/cancelled)
 *
 * The page is "data window": no controls, no editing — every interaction
 * is on the parent /datasets page. This keeps the URL stable for
 * thesis-screenshot purposes. */
export function DatasetDetail() {
  const t = useT();
  const { id } = useParams<{ id: string }>();
  const repoID = id ? Number(id) : NaN;

  const q = useQuery({
    queryKey: ["dataset", repoID],
    queryFn: () => fetchDatasetDetail(repoID),
    enabled: Number.isFinite(repoID),
    refetchInterval: 10_000,
  });

  if (!Number.isFinite(repoID)) {
    return <EmptyState title="Invalid repository id" hint="The URL must be /datasets/<numeric-id>." />;
  }
  if (q.isLoading) return <EmptyState title={t("common.loading")} />;
  if (q.isError || !q.data) {
    return (
      <EmptyState
        title="Could not load dataset."
        hint="The repository may have been removed, or the API is unavailable."
        action={<Link to="/datasets">{t("datasets.detail.back")}</Link>}
      />
    );
  }

  const d = q.data;
  const repo = d.repo;
  const bucketData = d.duration_buckets.map((b) => ({ label: b.label, value: Number(b.count) }));

  return (
    <>
      <PageHeader
        title={`${repo.owner}/${repo.name}`}
        subtitle={`${repo.runs_count.toLocaleString()} runs · ${repo.jobs_count.toLocaleString()} jobs`}
        actions={
          <div style={{ display: "flex", gap: "var(--s-3)", alignItems: "center" }}>
            <a
              href={datasetExportCSVURL(repo.id)}
              style={{ color: "var(--text-secondary)", textDecoration: "none", fontSize: "var(--fs-13)" }}
            >
              {t("datasets.export_csv")}
            </a>
            <Link to="/datasets" style={{ color: "var(--text-secondary)" }}>{t("datasets.detail.back")}</Link>
          </div>
        }
      />

      <div style={{ display: "flex", gap: "var(--s-3)", marginBottom: "var(--s-4)", alignItems: "center" }}>
        <StatusChip status={repo.status} />
        {repo.oldest_run_at && repo.newest_run_at && (
          <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-tertiary)" }}>
            {repo.oldest_run_at.slice(0, 10)} → {repo.newest_run_at.slice(0, 10)}
          </span>
        )}
        {repo.tracked_branches.length > 0 && (
          <span className="mono" style={{ fontSize: "var(--fs-12)", color: "var(--text-tertiary)" }}>
            tracked: {repo.tracked_branches.join(", ")}
          </span>
        )}
      </div>

      <Card>
        <SectionTitle>{t("datasets.detail.duration_dist")}</SectionTitle>
        <BarChart data={bucketData} width={720} height={220} format={(v) => v.toLocaleString()} />
      </Card>

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-3)", marginTop: "var(--s-3)" }}>
        <Card>
          <SectionTitle>{t("datasets.detail.top_workflows")}</SectionTitle>
          <table style={tableStyle}>
            <thead>
              <tr>
                <Th>workflow</Th>
                <Th right>runs</Th>
                <Th right>p50</Th>
                <Th right>p95</Th>
              </tr>
            </thead>
            <tbody>
              {d.top_workflows.map((w) => (
                <tr key={w.name}>
                  <Td mono>{w.name}</Td>
                  <Td right mono>{w.runs.toLocaleString()}</Td>
                  <Td right mono>{formatDuration(w.p50_sec)}</Td>
                  <Td right mono>{formatDuration(w.p95_sec)}</Td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>

        <Card>
          <SectionTitle>{t("datasets.detail.top_jobs")}</SectionTitle>
          <table style={tableStyle}>
            <thead>
              <tr>
                <Th>job</Th>
                <Th right>runs</Th>
                <Th right>mean</Th>
                <Th right>p50</Th>
              </tr>
            </thead>
            <tbody>
              {d.top_jobs.map((j) => (
                <tr key={j.name}>
                  <Td mono>{j.name}</Td>
                  <Td right mono>{j.runs.toLocaleString()}</Td>
                  <Td right mono>{formatDuration(j.mean_sec)}</Td>
                  <Td right mono>{formatDuration(j.p50_sec)}</Td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-3)", marginTop: "var(--s-3)" }}>
        <Card>
          <SectionTitle>{t("datasets.detail.branches")}</SectionTitle>
          <table style={tableStyle}>
            <thead>
              <tr>
                <Th>branch</Th>
                <Th right>runs</Th>
                <Th right>mean</Th>
              </tr>
            </thead>
            <tbody>
              {d.branch_breakdown.map((b) => (
                <tr key={b.branch}>
                  <Td mono>{b.branch}</Td>
                  <Td right mono>{b.runs.toLocaleString()}</Td>
                  <Td right mono>{formatDuration(b.mean_sec)}</Td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>

        <Card>
          <SectionTitle>{t("datasets.detail.conclusions")}</SectionTitle>
          <BarChart
            width={420}
            height={220}
            data={Object.entries(d.conclusion_counts).map(([k, v]) => ({ label: k, value: Number(v) }))}
            format={(v) => v.toLocaleString()}
          />
        </Card>
      </div>

      <div style={{ marginTop: "var(--s-3)" }}>
        <PushRecommendationsCard repoID={repoID} />
      </div>

      <div style={{ marginTop: "var(--s-3)" }}>
        <FeaturePreview repoID={repoID} />
      </div>
    </>
  );
}

/* PushRecommendationsCard — wraps PushHeatmap with title, hint, and
 * loading/empty states. Uses the browser's IANA timezone so the
 * thesis author sees Moscow office hours (or whatever local zone) by
 * default — falls back to UTC when Intl is unavailable. */
function PushRecommendationsCard({ repoID }: { repoID: number }) {
  const t = useT();
  const tz = (() => {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
    } catch {
      return "UTC";
    }
  })();

  const q = useQuery({
    queryKey: ["pushrec", repoID, tz],
    queryFn: () => fetchPushRecommendations(repoID, { days: 90, tz }),
    refetchInterval: 60_000,
  });

  return (
    <Card>
      <SectionTitle>{t("datasets.pushrec.title")}</SectionTitle>
      <p
        style={{
          color: "var(--text-tertiary)",
          fontSize: "var(--fs-12)",
          margin: "0 0 var(--s-3) 0",
          maxWidth: 760,
          lineHeight: 1.5,
        }}
      >
        {t("datasets.pushrec.hint")}
      </p>
      {q.isLoading && (
        <p style={{ color: "var(--text-secondary)", fontSize: "var(--fs-13)" }}>
          {t("common.loading")}
        </p>
      )}
      {q.data && q.data.overall.sample_count === 0 && (
        <p style={{ color: "var(--text-tertiary)", fontSize: "var(--fs-13)", margin: 0 }}>
          {t("datasets.pushrec.empty")}
        </p>
      )}
      {q.data && q.data.overall.sample_count > 0 && <PushHeatmap data={q.data} />}
    </Card>
  );
}

/* FeaturePreview — first N rows of materialised features for this repo.
 * Job-name filter narrows to one workflow's matrix, useful when the
 * user wants to inspect what the model sees per pipeline. */
function FeaturePreview({ repoID }: { repoID: number }) {
  const t = useT();
  const [jobName, setJobName] = useState<string>("");
  const q = useQuery({
    queryKey: ["features-preview", repoID, jobName],
    queryFn: () => fetchFeaturePreview(repoID, { limit: 50, jobName: jobName || undefined }),
  });

  // Collect the union of feature names from all rows so columns are
  // stable. Limit to the most common 12 columns to keep horizontal
  // scroll manageable.
  const cols: string[] = (() => {
    if (!q.data) return [];
    const counts = new Map<string, number>();
    for (const r of q.data.rows) {
      for (const k of Object.keys(r.features || {})) counts.set(k, (counts.get(k) ?? 0) + 1);
    }
    return Array.from(counts.entries())
      .sort((a, b) => b[1] - a[1])
      .slice(0, 12)
      .map(([k]) => k);
  })();

  return (
    <Card>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "var(--s-2)" }}>
        <SectionTitle>{t("datasets.detail.feature_preview")}</SectionTitle>
        <input
          value={jobName}
          onChange={(e) => setJobName(e.target.value)}
          placeholder={t("datasets.detail.feature_preview.filter")}
          spellCheck={false}
          style={{
            height: 28, padding: "0 var(--s-2)",
            background: "var(--bg-base)",
            border: "1px solid var(--border-subtle)",
            borderRadius: "var(--r-6)",
            color: "var(--text-primary)",
            fontFamily: "var(--font-mono)",
            fontSize: "var(--fs-12)",
            outline: "none",
            width: 240,
          }}
        />
      </div>
      {q.isLoading && <p style={{ color: "var(--text-secondary)", fontSize: "var(--fs-13)" }}>{t("common.loading")}</p>}
      {q.data && q.data.rows.length === 0 && (
        <p style={{ color: "var(--text-tertiary)", fontSize: "var(--fs-13)", margin: 0 }}>
          {t("datasets.detail.feature_preview.empty")}
        </p>
      )}
      {q.data && q.data.rows.length > 0 && (
        <div style={{ overflowX: "auto" }}>
          <table style={tableStyle}>
            <thead>
              <tr>
                <Th>job_name</Th>
                <Th right>duration</Th>
                {cols.map((c) => <Th key={c} right>{c.length > 18 ? c.slice(0, 16) + "…" : c}</Th>)}
              </tr>
            </thead>
            <tbody>
              {q.data.rows.map((r) => (
                <tr key={r.job_id}>
                  <Td mono>{r.job_name.length > 36 ? r.job_name.slice(0, 34) + "…" : r.job_name}</Td>
                  <Td right mono>{r.duration_sec !== undefined ? formatDuration(r.duration_sec) : "—"}</Td>
                  {cols.map((c) => {
                    const v = r.features?.[c];
                    return (
                      <Td key={c} right mono>
                        {typeof v === "number" ? v.toFixed(2) : v == null ? "—" : String(v)}
                      </Td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="caps"
      style={{
        color: "var(--text-tertiary)",
        marginBottom: "var(--s-2)",
        fontSize: "var(--fs-12)",
      }}
    >
      {children}
    </div>
  );
}

const tableStyle: React.CSSProperties = {
  width: "100%",
  borderCollapse: "collapse",
  fontSize: "var(--fs-12)",
};
function Th({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <th
      style={{
        textAlign: right ? "right" : "left",
        padding: "6px 8px",
        borderBottom: "1px solid var(--border-subtle)",
        color: "var(--text-tertiary)",
        fontWeight: 500,
        textTransform: "uppercase",
        letterSpacing: "0.06em",
        fontFamily: "var(--font-mono)",
      }}
    >
      {children}
    </th>
  );
}
function Td({ children, right, mono }: { children: React.ReactNode; right?: boolean; mono?: boolean }) {
  return (
    <td
      className={mono ? "mono" : undefined}
      style={{
        textAlign: right ? "right" : "left",
        padding: "5px 8px",
        borderBottom: "1px solid var(--border-subtle)",
      }}
    >
      {children}
    </td>
  );
}
