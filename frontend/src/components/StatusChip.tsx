type Status =
  | "queued"
  | "running"
  | "done"
  | "failed"
  | "cancelled"
  | "idle"
  | "synced"
  | "fetching"
  | "error"
  | "paused";

/* Mono small-caps pill. Status semantics, not decoration.
 *
 * Colour map keeps neutrals (idle / queued / done / cancelled) inert;
 * only failure states light up. This prevents the dashboard from
 * looking like a disco — by the time a user looks at it, the active
 * items should draw the eye, not the backlog. */
const PALETTE: Record<Status, { fg: string; bg: string; border?: string }> = {
  idle:      { fg: "var(--text-tertiary)", bg: "transparent",     border: "var(--border-subtle)" },
  queued:    { fg: "var(--text-secondary)", bg: "var(--bg-overlay)" },
  fetching:  { fg: "var(--info)",   bg: "var(--info-soft)" },
  running:   { fg: "var(--info)",   bg: "var(--info-soft)" },
  done:      { fg: "var(--ok)",     bg: "var(--ok-soft)" },
  synced:    { fg: "var(--ok)",     bg: "var(--ok-soft)" },
  failed:    { fg: "var(--err)",    bg: "var(--err-soft)" },
  error:     { fg: "var(--err)",    bg: "var(--err-soft)" },
  paused:    { fg: "var(--warn)",   bg: "var(--warn-soft)" },
  // `cancelled` shares the neutral palette with `idle` — same intent
  // (this row has no claim on attention) but kept as a distinct
  // chip label so users can tell "we never started" from "we gave up".
  cancelled: { fg: "var(--text-tertiary)", bg: "transparent",     border: "var(--border-subtle)" },
};

export function StatusChip({ status }: { status: Status }) {
  const p = PALETTE[status];
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        fontFamily: "var(--font-mono)",
        fontSize: 11,
        fontWeight: 500,
        textTransform: "uppercase",
        letterSpacing: "0.08em",
        padding: "2px 8px",
        borderRadius: "var(--r-pill)",
        color: p.fg,
        background: p.bg,
        border: p.border ? `1px solid ${p.border}` : "none",
      }}
    >
      {status}
    </span>
  );
}
