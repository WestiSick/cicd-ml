import { useState } from "react";
import { toast } from "sonner";
import { useMutation } from "@tanstack/react-query";

import { Button } from "@/components/Button";
import { startSetup } from "@/api/setup";
import { ApiError } from "@/api/client";
import { useActiveBootstrap } from "@/hooks/useActiveBootstrap";
import { LanguageSwitcher, useT } from "@/i18n";
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
  { id: "linear",   labelKey: "setup.algo.linear",   defaultOn: true,  costKey: "setup.cost.fast" },
  { id: "rf",       labelKey: "setup.algo.rf",       defaultOn: true,  costKey: "setup.cost.fast" },
  { id: "xgboost",  labelKey: "setup.algo.xgboost",  defaultOn: true,  costKey: "setup.cost.med"  },
  { id: "lightgbm", labelKey: "setup.algo.lightgbm", defaultOn: true,  costKey: "setup.cost.med"  },
  { id: "mlp",      labelKey: "setup.algo.mlp",      defaultOn: false, costKey: "setup.cost.med"  },
  { id: "lstm",     labelKey: "setup.algo.lstm",     defaultOn: false, costKey: "setup.cost.slow" },
] as const;

/* /setup — first-run onboarding.
 *
 * Two visual modes:
 *   - "form"    : the four-section wizard (token, repos, history, models)
 *   - "progress": live progress while the bootstrap chain runs
 *
 * Mode is derived from server state via useActiveBootstrap(), not from
 * local React state. That's deliberate: a page reload during setup
 * used to drop the user back on the empty form, even though
 * collection was still running in the background. The hook surfaces
 * the in-flight bootstrap row and the UI follows.
 *
 * `optimisticBootstrapId` covers the brief window between the
 * mutation's success and the next poll of useActiveBootstrap — without
 * it the user would see the form for a fraction of a second after
 * clicking "Start setup".
 *
 * Top-right corner hosts a Language switcher — the user picks once
 * here and the preference travels via localStorage to every other
 * page. */
export function Setup() {
  const t = useT();
  const active = useActiveBootstrap();
  const [token, setToken] = useState("");
  const [selectedRepos, setSelectedRepos] = useState<string[]>(
    SEED_REPOS.map((r) => `${r.owner}/${r.name}`)
  );
  const [months, setMonths] = useState<3 | 6 | 12>(6);
  const [models, setModels] = useState<string[]>(
    ALGORITHMS.filter((a) => a.defaultOn).map((a) => a.id)
  );
  const [optimisticBootstrapId, setOptimisticBootstrapId] = useState<number | null>(null);

  const mutation = useMutation({
    mutationFn: startSetup,
    onSuccess: (resp) => {
      toast.success(t("setup.toast.queued"), { description: resp.message });
      setOptimisticBootstrapId(resp.bg_job_id);
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) {
        toast.error(err.message, { description: err.userAction });
      } else {
        toast.error(t("setup.toast.queued"));
      }
    },
  });

  // Prefer the server's view (survives reload). Fall back to the
  // optimistic id only for the few hundred ms before the next poll.
  const bootstrapId = active.job?.id ?? optimisticBootstrapId;

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
      toast.error(t("setup.toast.pick_repo"));
      return;
    }
    if (models.length === 0) {
      toast.error(t("setup.toast.pick_model"));
      return;
    }
    mutation.mutate({
      github_token: token || undefined,
      repos: selectedRepos,
      history_months: months,
      models,
    });
  }

  // Don't flash the form during the initial /api/bg-jobs load — that's
  // what causes the "form for a moment, then progress" jank on reload.
  if (active.isLoading) {
    return null;
  }

  if (bootstrapId !== null) {
    return <SetupProgress bootstrapId={bootstrapId} />;
  }

  return (
    <div style={{ position: "relative" }}>
      <div style={{ position: "absolute", top: "var(--s-4)", right: "var(--s-6)", zIndex: 1 }}>
        <LanguageSwitcher />
      </div>

      <div
        style={{
          maxWidth: 760,
          margin: "0 auto",
          padding: "var(--s-12) var(--s-6) var(--s-16)",
        }}
      >
        <div style={{ marginBottom: "var(--s-8)" }}>
          <div className="caps" style={{ color: "var(--accent)", marginBottom: "var(--s-2)" }}>
            {t("setup.label")}
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
            {t("setup.title_line1")}
            <br />
            {t("setup.title_line2")}
          </h1>
          <p
            style={{
              margin: "var(--s-3) 0 0",
              color: "var(--text-secondary)",
              fontSize: "var(--fs-16)",
              maxWidth: 560,
            }}
          >
            {t("setup.intro")}
          </p>
        </div>

        <Section label="01" title={t("setup.section.token")} hint={t("setup.section.token.hint")}>
          <input
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="ghp_••••••••••••"
            spellCheck={false}
            style={inputStyle}
          />
        </Section>

        <Section label="02" title={t("setup.section.repos")} hint={t("setup.section.repos.hint")}>
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

        <Section label="03" title={t("setup.section.window")} hint={t("setup.section.window.hint")}>
          <div style={{ display: "flex", gap: "var(--s-2)" }}>
            {([3, 6, 12] as const).map((m) => (
              <button key={m} onClick={() => setMonths(m)} style={pillStyle(months === m)}>
                {t("setup.months", { n: m })}
              </button>
            ))}
          </div>
        </Section>

        <Section label="04" title={t("setup.section.models")} hint={t("setup.section.models.hint")}>
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
                  <span style={{ flex: 1, fontSize: "var(--fs-13)" }}>{t(a.labelKey)}</span>
                  <span className="mono caps" style={{ color: "var(--text-tertiary)", fontSize: 11 }}>
                    {t(a.costKey)}
                  </span>
                </label>
              );
            })}
          </div>
        </Section>

        <div
          style={{ display: "flex", justifyContent: "flex-end", marginTop: "var(--s-8)", gap: "var(--s-2)" }}
        >
          <Button variant="primary" onClick={submit} loading={mutation.isPending}>
            {t("setup.start")}
          </Button>
        </div>
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
