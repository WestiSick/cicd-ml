import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

/* CommandPalette — Cmd/Ctrl+K opens a fuzzy-search list of jump targets.
 *
 * Mirrors what Linear / Vercel / Datadog do: keyboard-first navigation
 * across every page in the app, plus the most common actions. This is the
 * single highest-leverage UX add for a "feels like a real tool" feel —
 * once you start using it you stop using the sidebar.
 *
 * Search is naive substring match (case-insensitive) — fancy fuzzy
 * (fzf-style) is unnecessary at 15 commands. Up/Down/Enter/Esc.
 *
 * Why hand-rolled rather than cmdk: zero new dependencies, ~120 lines,
 * complete control over styling. cmdk's defaults clash with the
 * editorial palette in this app.
 */
type Command = {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
};

export function CommandPalette() {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [cursor, setCursor] = useState(0);

  // Global Cmd/Ctrl+K toggle. Also "/" to open (Linear convention).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const meta = e.metaKey || e.ctrlKey;
      if (meta && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
        setQ("");
        setCursor(0);
        return;
      }
      // Quick-jump shortcuts (Linear-style "G then key"). We use single-letter
      // combos for simplicity rather than tracking the "G" prefix sequence.
      if (e.altKey && !e.shiftKey && !meta) {
        const map: Record<string, string> = {
          "d": "/dashboard",
          "s": "/datasets",
          "e": "/experiments",
          "i": "/simulator",
          "a": "/admin",
        };
        const target = map[e.key.toLowerCase()];
        if (target) {
          e.preventDefault();
          navigate(target);
        }
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [navigate]);

  const commands: Command[] = [
    { id: "go.dashboard",   label: "Go to Dashboard",   hint: "Alt+D · live queue and KPIs",       run: () => navigate("/dashboard") },
    { id: "go.history",     label: "Go to History",     hint: "persistent predict-vs-actual log",  run: () => navigate("/history") },
    { id: "go.datasets",    label: "Go to Datasets",    hint: "Alt+S · tracked repositories",      run: () => navigate("/datasets") },
    { id: "go.experiments", label: "Go to Experiments", hint: "Alt+E · trained models and runs",   run: () => navigate("/experiments") },
    { id: "go.simulator",   label: "Go to Simulator",   hint: "Alt+I · strategy comparison",       run: () => navigate("/simulator") },
    { id: "go.admin",       label: "Go to Admin",       hint: "Alt+A · settings, webhooks, health",run: () => navigate("/admin") },
    { id: "admin.settings", label: "Admin → Settings",  hint: "strategy, weights, PAT",            run: () => navigate("/admin#settings") },
    { id: "admin.activity", label: "Admin → Activity log", hint: "recent user actions",             run: () => navigate("/admin#activity") },
    { id: "admin.health",   label: "Admin → System health", hint: "service status",                  run: () => navigate("/admin#system-health") },
    { id: "admin.webhooks", label: "Admin → Webhooks",  hint: "GitHub delivery log",                run: () => navigate("/admin#webhooks") },
    { id: "exp.train",      label: "Experiments → Train new model", hint: "open the train form",    run: () => navigate("/experiments") },
    { id: "ds.add",         label: "Datasets → Add repository",     hint: "paste a GitHub URL",     run: () => navigate("/datasets") },
  ];

  const filtered = commands.filter(
    (c) => !q || c.label.toLowerCase().includes(q.toLowerCase()) || (c.hint ?? "").toLowerCase().includes(q.toLowerCase()),
  );

  // Keep cursor in range as the filter changes.
  useEffect(() => {
    if (cursor >= filtered.length) setCursor(0);
  }, [filtered.length, cursor]);

  function onKey(e: React.KeyboardEvent) {
    if (e.key === "Escape") { setOpen(false); return; }
    if (e.key === "ArrowDown") { e.preventDefault(); setCursor((c) => Math.min(c + 1, filtered.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setCursor((c) => Math.max(c - 1, 0)); }
    else if (e.key === "Enter") {
      e.preventDefault();
      filtered[cursor]?.run();
      setOpen(false);
    }
  }

  if (!open) return null;
  return (
    <div
      style={{
        position: "fixed", inset: 0, background: "rgba(0,0,0,0.45)",
        display: "flex", alignItems: "flex-start", justifyContent: "center",
        paddingTop: "10vh", zIndex: 200,
      }}
      onClick={() => setOpen(false)}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: "min(560px, 92%)",
          background: "var(--bg-elevated)",
          border: "1px solid var(--border-strong)",
          borderRadius: "var(--r-8)",
          overflow: "hidden",
        }}
      >
        <input
          autoFocus
          value={q}
          onChange={(e) => { setQ(e.target.value); setCursor(0); }}
          onKeyDown={onKey}
          placeholder="Type to filter — Up/Down/Enter"
          spellCheck={false}
          style={{
            width: "100%", height: 44, padding: "0 var(--s-4)",
            border: "none", outline: "none",
            background: "transparent", color: "var(--text-primary)",
            fontFamily: "var(--font-mono)", fontSize: "var(--fs-14)",
            borderBottom: "1px solid var(--border-subtle)",
          }}
        />
        <div style={{ maxHeight: "60vh", overflow: "auto" }}>
          {filtered.length === 0 ? (
            <div style={{ padding: "var(--s-4)", color: "var(--text-tertiary)", fontSize: "var(--fs-13)" }}>
              No matching commands.
            </div>
          ) : (
            filtered.map((c, i) => (
              <button
                key={c.id}
                onMouseEnter={() => setCursor(i)}
                onClick={() => { c.run(); setOpen(false); }}
                style={{
                  display: "block",
                  width: "100%", textAlign: "left",
                  padding: "10px var(--s-4)",
                  background: i === cursor ? "var(--bg-overlay)" : "transparent",
                  border: "none",
                  color: "var(--text-primary)",
                  fontFamily: "var(--font-mono)", fontSize: "var(--fs-13)",
                  cursor: "pointer",
                }}
              >
                <span>{c.label}</span>
                {c.hint && (
                  <span style={{ marginLeft: 12, color: "var(--text-tertiary)", fontSize: "var(--fs-11)" }}>
                    {c.hint}
                  </span>
                )}
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
