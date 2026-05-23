import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { StatusChip } from "@/components/StatusChip";
import { Button } from "@/components/Button";
import { EmptyState } from "@/components/EmptyState";
import { ApiError } from "@/api/client";
import { addRepo, listRepos, syncRepo, type Repo } from "@/api/repos";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useT } from "@/i18n";

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

  // A "Sync" action makes sense whenever the repo isn't actively being
  // fetched. fetching/running cards already show progress — extra
  // button would only let the user double-queue work for nothing.
  const canSync = repo.status !== "fetching";

  return (
    <Card>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "var(--s-2)" }}>
        <div className="mono" style={{ fontSize: "var(--fs-14)", fontWeight: 500 }}>
          {repo.owner}/{repo.name}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: "var(--s-2)" }}>
          {canSync && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => sync.mutate()}
              loading={sync.isPending}
            >
              {repo.status === "idle" ? t("common.start_sync") : t("common.sync")}
            </Button>
          )}
          <StatusChip status={repo.status} />
        </div>
      </div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr 1fr",
          gap: "var(--s-3)",
          marginTop: "var(--s-3)",
        }}
      >
        <Stat label={t("datasets.card.runs")} value={repo.runs_count.toLocaleString()} mono />
        <Stat label={t("datasets.card.jobs")} value={repo.jobs_count.toLocaleString()} mono />
        <Stat
          label={t("datasets.card.added")}
          value={new Date(repo.added_at).toISOString().slice(0, 10)}
          mono
          small
        />
      </div>
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
    </Card>
  );
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
