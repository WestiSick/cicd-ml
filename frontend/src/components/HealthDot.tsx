import { useHealth } from "@/hooks/useHealth";

/* Top-bar service status indicator.
 *
 * Three states: ok / degraded / down. Clicking it routes to /admin →
 * System health for the breakdown. Intentionally tiny — it's status, not
 * decoration; we don't want a colourful "I'm alive" badge.
 */
export function HealthDot() {
  const { state, label } = useHealth();
  const color =
    state === "ok"
      ? "var(--ok)"
      : state === "degraded"
      ? "var(--warn)"
      : "var(--err)";

  return (
    <a
      href="/admin#system-health"
      title={label}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        fontFamily: "var(--font-mono)",
        fontSize: "var(--fs-12)",
        color: "var(--text-tertiary)",
        textTransform: "uppercase",
        letterSpacing: "0.06em",
      }}
    >
      <span
        style={{
          display: "inline-block",
          width: 6,
          height: 6,
          borderRadius: "var(--r-pill)",
          background: color,
          boxShadow: `0 0 0 3px ${state === "ok" ? "var(--ok-soft)" : state === "degraded" ? "var(--warn-soft)" : "var(--err-soft)"}`,
        }}
      />
      {state}
    </a>
  );
}
