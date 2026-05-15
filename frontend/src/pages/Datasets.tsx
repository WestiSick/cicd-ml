import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { PageHeader } from "@/components/PageHeader";
import { Card } from "@/components/Card";
import { StatusChip } from "@/components/StatusChip";
import { Button } from "@/components/Button";
import { EmptyState } from "@/components/EmptyState";
import { ApiError } from "@/api/client";
import { addRepo, listRepos, type Repo } from "@/api/repos";

export function Datasets() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["repos"], queryFn: listRepos });

  const [showAdd, setShowAdd] = useState(false);

  return (
    <>
      <PageHeader
        title="Datasets"
        subtitle="Tracked repositories and the historical jobs collected for training."
        actions={
          <>
            <Button variant="ghost" disabled>
              Compute features
            </Button>
            <Button variant="primary" onClick={() => setShowAdd(true)}>
              Add repository
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
          title="No repositories tracked yet."
          hint="Add one to start collecting CI history. Public repositories work without a token but slowly; supply a PAT in /admin for the full 5000 req/h limit."
          action={<Button variant="primary" onClick={() => setShowAdd(true)}>Add your first repository</Button>}
        />
      )}

      {q.data && q.data.length > 0 && (
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-3)" }}>
          {q.data.map((r) => <RepoCard key={r.id} repo={r} />)}
        </div>
      )}
    </>
  );
}

function RepoCard({ repo }: { repo: Repo }) {
  return (
    <Card interactive>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div className="mono" style={{ fontSize: "var(--fs-14)", fontWeight: 500 }}>
          {repo.owner}/{repo.name}
        </div>
        <StatusChip status={repo.status} />
      </div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr 1fr",
          gap: "var(--s-3)",
          marginTop: "var(--s-3)",
        }}
      >
        <Stat label="Runs" value={repo.runs_count.toLocaleString()} mono />
        <Stat label="Jobs" value={repo.jobs_count.toLocaleString()} mono />
        <Stat
          label="Added"
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
  const [url, setUrl] = useState("");

  const m = useMutation({
    mutationFn: () => addRepo({ url }),
    onSuccess: (r) => {
      toast.success(`Repository added: ${r.owner}/${r.name}`);
      onAdded();
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) {
        toast.error(err.message, { description: err.userAction });
      } else {
        toast.error("Could not add repository.");
      }
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
        Add repository
      </div>
      <div style={{ display: "flex", gap: "var(--s-2)" }}>
        <input
          autoFocus
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://github.com/owner/repo"
          spellCheck={false}
          style={{
            flex: 1,
            height: 32,
            background: "var(--bg-base)",
            border: "1px solid var(--border-subtle)",
            borderRadius: "var(--r-6)",
            padding: "0 var(--s-3)",
            color: "var(--text-primary)",
            fontFamily: "var(--font-mono)",
            fontSize: "var(--fs-13)",
            outline: "none",
          }}
          onKeyDown={(e) => e.key === "Enter" && m.mutate()}
        />
        <Button variant="secondary" onClick={onClose}>Cancel</Button>
        <Button variant="primary" onClick={() => m.mutate()} loading={m.isPending} disabled={!url}>
          Add
        </Button>
      </div>
    </div>
  );
}
