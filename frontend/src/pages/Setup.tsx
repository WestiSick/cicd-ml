import { useState } from "react";
import { toast } from "sonner";
import { useMutation } from "@tanstack/react-query";

import { Button } from "@/components/Button";
import { startSetup } from "@/api/setup";
import { ApiError } from "@/api/client";
import { SetupProgress } from "./SetupProgress";

const SEED_REPOS = [
  { owner: "vitejs", name: "vite" },
  { owner: "prometheus", name: "prometheus" },
  { owner: "gin-gonic", name: "gin" },
  { owner: "fastapi", name: "fastapi" },
  { owner: "pandas-dev", name: "pandas" },
  { owner: "golang", name: "go" },
  { owner: "pallets", name: "flask" },
];

const ALGORITHMS = [
  { id: "linear",   label: "Linear regression",   default: true,  cost: "fast" },
  { id: "rf",       label: "Random Forest",       default: true,  cost: "fast" },
  { id: "xgboost",  label: "XGBoost",             default: true,  cost: "med"  },
  { id: "lightgbm", label: "LightGBM",            default: true,  cost: "med"  },
  { id: "mlp",      label: "MLP (PyTorch)",       default: false, cost: "med"  },
  { id: "lstm",     label: "LSTM (PyTorch)",      default: false, cost: "slow" },
];

/* /setup — first-run onboarding.
 *
 * Two visual modes:
 *   - "form"    : the four-section wizard (token, repos, history, models)
 *   - "progress": live progress while the bootstrap chain runs
 * Transition happens after POST /api/setup/start returns a bg_job_id.
 * The progress mode streams /ws/bg-jobs and renders one card per job.
 */
export function Setup() {
  const [token, setToken] = useState("");
  const [selectedRepos, setSelectedRepos] = useState<string[]>(
    SEED_REPOS.map((r) => `${r.owner}/${r.name}`)
  );
  const [months, setMonths] = useState<3 | 6 | 12>(6);
  const [models, setModels] = useState<string[]>(
    ALGORITHMS.filter((a) => a.default).map((a) => a.id)
  );
  const [bootstrapId, setBootstrapId] = useState<number | null>(null);

  const mutation = useMutation({
    mutationFn: startSetup,
    onSuccess: (resp) => {
      toast.success("Setup queued.", { description: resp.message });
      setBootstrapId(resp.bg_job_id);
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) {
        toast.error(err.message, { description: err.userAction });
      } else {
        toast.error("Could not start setup.");
      }
    },
  });

  function toggleRepo(slug: string) {
    setSelectedRepos((cur) =>
      cur.includes(slug) ? cur.filter((s) => s !== slug) : [...cur, slug]
    );
  }
  function toggleModel(id: string) {
    setModels((cur) => (cur.includes(id) ? cur.filter((m) => m !== id) : [...cur, id]));
  }

  function submit() {
    if (selectedRepos.length === 0) {
      toast.error("Pick at least one repository.");
      return;
    }
    if (models.length === 0) {
      toast.error("Pick at least one model to train.");
      return;
    }
    mutation.mutate({
      github_token: token || undefined,
      repos: selectedRepos,
      history_months: months,
      models,
    });
  }

  if (bootstrapId !== null) {
    return <SetupProgress bootstrapId={bootstrapId} />;
  }

  return (
    <div
      style={{
        maxWidth: 760,
        margin: "0 auto",
        padding: "var(--s-12) var(--s-6) var(--s-16)",
      }}
    >
      <div style={{ marginBottom: "var(--s-8)" }}>
        <div className="caps" style={{ color: "var(--accent)", marginBottom: "var(--s-2)" }}>
          Initial setup
        </div>
        <h1
          style={{
            margin: 0,
            fontSize: "var(--fs-40)",
            fontWeight: 500,
            letterSpacing: "-0.02em",
            lineHeight: 1.1,
          }}
        >
          Configure the dataset
          <br />
          and pre-train models.
        </h1>
        <p
          style={{
            margin: "var(--s-3) 0 0",
            color: "var(--text-secondary)",
            fontSize: "var(--fs-16)",
            maxWidth: 560,
          }}
        >
          This runs once. The system will collect CI history from GitHub
          Actions and train every selected model in the background. You can
          close the tab — progress resumes when you come back.
        </p>
      </div>

      <Section
        label="01"
        title="GitHub token"
        hint="Optional. Without it the API limit is 60 req/h (slow). With a personal access token (public_repo scope) it's 5000 req/h."
      >
        <input
          type="password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          placeholder="ghp_••••••••••••"
          spellCheck={false}
          style={inputStyle}
        />
      </Section>

      <Section
        label="02"
        title="Seed repositories"
        hint="These are public projects with high-quality CI history. Uncheck any you don't want."
      >
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-2)" }}>
          {SEED_REPOS.map((r) => {
            const slug = `${r.owner}/${r.name}`;
            const checked = selectedRepos.includes(slug);
            return (
              <label key={slug} style={rowStyle(checked)}>
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => toggleRepo(slug)}
                  style={checkboxStyle}
                />
                <span className="mono" style={{ fontSize: "var(--fs-13)" }}>
                  {slug}
                </span>
              </label>
            );
          })}
        </div>
      </Section>

      <Section
        label="03"
        title="History window"
        hint="How far back to fetch runs. Longer windows mean better models but more API calls."
      >
        <div style={{ display: "flex", gap: "var(--s-2)" }}>
          {([3, 6, 12] as const).map((m) => (
            <button key={m} onClick={() => setMonths(m)} style={pillStyle(months === m)}>
              {m} months
            </button>
          ))}
        </div>
      </Section>

      <Section
        label="04"
        title="Models to pre-train"
        hint="All run in sequence. LSTM is slowest — leave off if unsure."
      >
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--s-2)" }}>
          {ALGORITHMS.map((a) => {
            const checked = models.includes(a.id);
            return (
              <label key={a.id} style={rowStyle(checked)}>
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => toggleModel(a.id)}
                  style={checkboxStyle}
                />
                <span style={{ flex: 1, fontSize: "var(--fs-13)" }}>{a.label}</span>
                <span className="mono caps" style={{ color: "var(--text-tertiary)", fontSize: 11 }}>
                  {a.cost}
                </span>
              </label>
            );
          })}
        </div>
      </Section>

      <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "var(--s-8)", gap: "var(--s-2)" }}>
        <Button variant="primary" onClick={submit} loading={mutation.isPending}>
          Start setup
        </Button>
      </div>
    </div>
  );
}

function Section({
  label,
  title,
  hint,
  children,
}: {
  label: string;
  title: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      style={{
        display: "grid",
        gridTemplateColumns: "60px 1fr",
        gap: "var(--s-6)",
        padding: "var(--s-6) 0",
        borderTop: "1px solid var(--border-subtle)",
      }}
    >
      <div className="mono caps" style={{ color: "var(--text-tertiary)", paddingTop: 2 }}>
        {label}
      </div>
      <div>
        <div style={{ fontSize: "var(--fs-16)", fontWeight: 500, marginBottom: "var(--s-1)" }}>
          {title}
        </div>
        {hint && (
          <div
            style={{
              color: "var(--text-secondary)",
              fontSize: "var(--fs-13)",
              marginBottom: "var(--s-3)",
              maxWidth: 540,
            }}
          >
            {hint}
          </div>
        )}
        {children}
      </div>
    </section>
  );
}

const inputStyle: React.CSSProperties = {
  width: "100%",
  height: 36,
  background: "var(--bg-elevated)",
  border: "1px solid var(--border-subtle)",
  borderRadius: "var(--r-6)",
  padding: "0 var(--s-3)",
  color: "var(--text-primary)",
  fontFamily: "var(--font-mono)",
  fontSize: "var(--fs-13)",
  outline: "none",
};

function rowStyle(checked: boolean): React.CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    gap: "var(--s-2)",
    padding: "8px 12px",
    border: `1px solid ${checked ? "var(--border-strong)" : "var(--border-subtle)"}`,
    borderRadius: "var(--r-6)",
    background: checked ? "var(--bg-elevated)" : "transparent",
    cursor: "pointer",
    transition: "border-color var(--t-hover) var(--ease), background var(--t-hover) var(--ease)",
  };
}

const checkboxStyle: React.CSSProperties = { accentColor: "var(--accent)" };

function pillStyle(active: boolean): React.CSSProperties {
  return {
    height: 32,
    padding: "0 16px",
    background: active ? "var(--bg-elevated)" : "transparent",
    color: active ? "var(--text-primary)" : "var(--text-secondary)",
    border: `1px solid ${active ? "var(--border-strong)" : "var(--border-subtle)"}`,
    borderRadius: "var(--r-6)",
    fontSize: "var(--fs-13)",
    cursor: "pointer",
    boxShadow: active ? "inset 0 0 0 1px var(--accent-soft)" : undefined,
  };
}
