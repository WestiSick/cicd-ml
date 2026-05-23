import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { StatusChip } from "@/components/StatusChip";
import { Button } from "@/components/Button";
import { EmptyState } from "@/components/EmptyState";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ApiError } from "@/api/client";
import {
  addRepo,
  deleteRepo,
  listRepos,
  pauseRepo,
  resumeRepo,
  resyncRepo,
  syncRepo,
  type Repo,
} from "@/api/repos";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useT } from "@/i18n";
import { formatRelativeTime } from "@/lib/format";

export function Datasets() {
  const t = useT();
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["repos"],
    queryFn: listRepos,
    // Repos update as the collector reports progress — poll modestly so
    // the runs/jobs counters move while the user watches.
    refetchInterval: 5_000,
  });

  // Any bg-jobs update can affect repo counters (collect_history bumps
  // them on every page). Cheap to refresh on ws push.
  useWebSocket("/ws/bg-jobs", () => {
    qc.invalidateQueries({ queryKey: ["repos"] });
  });

  const [showAdd, setShowAdd] = useState(false);

  return (
    <>
      <PageHeader
        title={t("datasets.title")}
        subtitle={t("datasets.subtitle")}
        actions={
          <>
            <Button variant="ghost" disabled>
              {t("datasets.compute_features")}
            </Button>
            <Button variant="primary" onClick={() => setShowAdd(true)}>
              {t("datasets.add")}
            </Button>
          </>
        }
      />

      {showAdd && (
        <AddRepoBlock
          onClose={() => setShowAdd(false)}
          onAdded={() => {
            qc.invalidateQueries({ queryKey: ["repos"] });
            setShowAdd(false);
          }}
        />
      )}

      {q.isLoading && <Skeletons />}

      {q.isError && (
        <EmptyState
          title="Could not load repositories."
          hint="Make sure the API container is running and try again."
        />
      )}

      {q.data && q.data.length === 0 && !showAdd && (
        <EmptyState
          title={t("datasets.empty.title")}
          hint={t("datasets.empty.hint")}
          action={<Button variant="primary" onClick={() => setShowAdd(true)}>{t("datasets.empty.action")}</Button>}
        />
      )}

      {q.data && q.data.length > 0 && (
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-3)" }}>
          {q.data.map((r) => (
            <RepoCard
              key={r.id}
              repo={r}
              onSynced={() => qc.invalidateQueries({ queryKey: ["repos"] })}
            />
          ))}
        </div>
      )}
    </>
  );
}

function RepoCard({ repo, onSynced }: { repo: Repo; onSynced: () => void }) {
  const t = useT();
  const [confirmDelete, setConfirmDelete] = useState(false);

  const sync = useMutation({
    mutationFn: () => syncRepo(repo.id),
    onSuccess: (r) => {
      toast.success(t("datasets.toast.sync_queued", { slug: `${repo.owner}/${repo.name}` }), {
        description: r.message,
      });
      onSynced();
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("sync failed");
    },
  });

  const pause = useMutation({
    mutationFn: () => pauseRepo(repo.id),
    onSuccess: () => { toast.success(t("datasets.toast.paused")); onSynced(); },
    onError: (err: unknown) => err instanceof ApiError ? toast.error(err.message) : toast.error("pause failed"),
  });
  const resume = useMutation({
    mutationFn: () => resumeRepo(repo.id),
    onSuccess: () => { toast.success(t("datasets.toast.resumed")); onSynced(); },
    onError: (err: unknown) => err instanceof ApiError ? toast.error(err.message) : toast.error("resume failed"),
  });
  const resync = useMutation({
    mutationFn: () => resyncRepo(repo.id),
    onSuccess: () => { toast.success(t("datasets.toast.resync_queued")); onSynced(); },
    onError: (err: unknown) => err instanceof ApiError ? toast.error(err.message) : toast.error("resync failed"),
  });
  const remove = useMutation({
    mutationFn: () => deleteRepo(repo.id),
    onSuccess: (r) => {
      toast.success(t("datasets.toast.removed", { slug: `${repo.owner}/${repo.name}` }),
        { description: t("datasets.toast.removed_desc", { n: r.rows_deleted }) });
      onSynced();
    },
    onError: (err: unknown) => err instanceof ApiError ? toast.error(err.message) : toast.error("delete failed"),
  });

  // Sync is meaningful unless we're already fetching. paused → resume first.
  const canSync = repo.status !== "fetching" && repo.status !== "paused";

  return (
    <Card>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "var(--s-2)" }}>
        <Link
          to={`/datasets/${repo.id}`}
          className="mono"
          style={{
            fontSize: "var(--fs-14)",
            fontWeight: 500,
            color: "var(--text-primary)",
            textDecoration: "none",
          }}
        >
          {repo.owner}/{repo.name}
        </Link>
        <div style={{ display: "flex", alignItems: "center", gap: "var(--s-2)" }}>
          {canSync && (
            <Button size="sm" variant="ghost" onClick={() => sync.mutate()} loading={sync.isPending}>
              {repo.status === "idle" ? t("common.start_sync") : t("common.sync")}
            </Button>
          )}
          {repo.status === "paused" ? (
            <Button size="sm" variant="ghost" onClick={() => resume.mutate()} loading={resume.isPending}>
              {t("common.resume")}
            </Button>
          ) : (
            <Button size="sm" variant="ghost" onClick={() => pause.mutate()} loading={pause.isPending}>
              {t("common.pause")}
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={() => resync.mutate()} loading={resync.isPending}>
            {t("common.resync")}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(true)}>
            {t("common.remove")}
          </Button>
          <StatusChip status={repo.status} />
        </div>
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr 1fr 1fr",
          gap: "var(--s-3)",
          marginTop: "var(--s-3)",
        }}
      >
        <Stat label={t("datasets.card.runs")} value={repo.runs_count.toLocaleString()} mono />
        <Stat label={t("datasets.card.jobs")} value={repo.jobs_count.toLocaleString()} mono />
        <Stat
          label={t("datasets.card.coverage")}
          value={coverageLabel(repo)}
          mono
          small
        />
        <Stat
          label={t("datasets.card.last_synced")}
          value={repo.last_synced_at ? formatRelativeTime(repo.last_synced_at) : "—"}
          mono
          small
        />
      </div>

      {repo.tracked_branches && repo.tracked_branches.length > 0 && (
        <div
          className="mono"
          style={{
            marginTop: "var(--s-3)",
            fontSize: "var(--fs-12)",
            color: "var(--text-tertiary)",
          }}
        >
          {repo.tracked_branches.slice(0, 4).join(" · ")}
          {repo.tracked_branches.length > 4 && ` · +${repo.tracked_branches.length - 4}`}
        </div>
      )}

      {repo.last_error && (
        <div
          style={{
            marginTop: "var(--s-3)",
            padding: "6px 8px",
            border: "1px solid var(--err-soft)",
            borderRadius: "var(--r-6)",
            color: "var(--err)",
            fontSize: "var(--fs-12)",
            fontFamily: "var(--font-mono)",
          }}
        >
          {repo.last_error}
        </div>
      )}

      <ConfirmDialog
        open={confirmDelete}
        title={t("datasets.delete.title")}
        message={t("datasets.delete.message", { slug: `${repo.owner}/${repo.name}` })}
        confirmLabel={t("datasets.delete.confirm")}
        requireText={`${repo.owner}/${repo.name}`}
        danger
        onCancel={() => setConfirmDelete(false)}
        onConfirm={() => {
          setConfirmDelete(false);
          remove.mutate();
        }}
      />
    </Card>
  );
}

function coverageLabel(repo: Repo): string {
  if (!repo.oldest_run_at || !repo.newest_run_at) return "—";
  const o = repo.oldest_run_at.slice(0, 10);
  const n = repo.newest_run_at.slice(0, 10);
  return `${o} → ${n}`;
}

function Stat({
  label,
  value,
  mono,
  small,
}: {
  label: string;
  value: string;
  mono?: boolean;
  small?: boolean;
}) {
  return (
    <div>
      <div className="caps" style={{ color: "var(--text-tertiary)" }}>{label}</div>
      <div
        className={mono ? "mono" : undefined}
        style={{ fontSize: small ? "var(--fs-12)" : "var(--fs-16)", marginTop: 2 }}
      >
        {value}
      </div>
    </div>
  );
}

function Skeletons() {
  return (
    <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-3)" }}>
      {[0, 1, 2, 3].map((i) => (
        <div
          key={i}
          style={{
            background: "var(--bg-elevated)",
            border: "1px solid var(--border-subtle)",
            borderRadius: "var(--r-8)",
            height: 110,
          }}
        />
      ))}
    </div>
  );
}

function AddRepoBlock({
  onClose,
  onAdded,
}: {
  onClose: () => void;
  onAdded: () => void;
}) {
  const t = useT();
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [months, setMonths] = useState<3 | 6 | 12>(6);

  const m = useMutation({
    mutationFn: () =>
      addRepo({
        url,
        history_months: months,
        github_token: token || undefined,
      }),
    onSuccess: (r) => {
      toast.success(t("datasets.toast.added", { slug: `${r.owner}/${r.name}` }), {
        description: t("datasets.toast.added_desc"),
      });
      onAdded();
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("add failed");
    },
  });

  return (
    <div
      style={{
        marginBottom: "var(--s-4)",
        padding: "var(--s-4)",
        background: "var(--bg-elevated)",
        border: "1px solid var(--border-strong)",
        borderRadius: "var(--r-8)",
      }}
    >
      <div className="caps" style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}>
        {t("datasets.add.title")}
      </div>

      <div style={{ display: "flex", gap: "var(--s-2)", marginBottom: "var(--s-3)" }}>
        <input
          autoFocus
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder={t("datasets.add.url_placeholder")}
          spellCheck={false}
          style={inputStyle}
          onKeyDown={(e) => e.key === "Enter" && url && m.mutate()}
        />
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-3)" }}>
        <div>
          <div className="caps" style={labelStyle}>{t("datasets.add.window")}</div>
          <div style={{ display: "flex", gap: "var(--s-2)" }}>
            {([3, 6, 12] as const).map((n) => (
              <button key={n} type="button" onClick={() => setMonths(n)} style={pillStyle(months === n)}>
                {t("setup.months", { n })}
              </button>
            ))}
          </div>
        </div>
        <div>
          <div className="caps" style={labelStyle}>{t("datasets.add.token")}</div>
          <input
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder={t("datasets.add.token_placeholder")}
            spellCheck={false}
            style={inputStyle}
          />
        </div>
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end", gap: "var(--s-2)", marginTop: "var(--s-4)" }}>
        <Button variant="secondary" onClick={onClose}>{t("common.cancel")}</Button>
        <Button variant="primary" onClick={() => m.mutate()} loading={m.isPending} disabled={!url}>
          {t("datasets.add.submit")}
        </Button>
      </div>
    </div>
  );
}

const inputStyle: React.CSSProperties = {
  flex: 1,
  width: "100%",
  height: 32,
  background: "var(--bg-base)",
  border: "1px solid var(--border-subtle)",
  borderRadius: "var(--r-6)",
  padding: "0 var(--s-3)",
  color: "var(--text-primary)",
  fontFamily: "var(--font-mono)",
  fontSize: "var(--fs-13)",
  outline: "none",
};

const labelStyle: React.CSSProperties = { color: "var(--text-tertiary)", marginBottom: "var(--s-2)" };

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
