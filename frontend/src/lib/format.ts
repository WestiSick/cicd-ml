/* Shared formatters for numbers shown to humans in the UI.
 *
 * Centralising these avoids the usual drift where one page shows "60s",
 * another "1m 0s", and a third "1.0 min". The thesis screenshots need
 * consistent rendering across every page, so all duration/percent
 * displays route through here.
 */

// formatDuration → compact "1h 22m 03s" / "4m 12s" / "47s" / "0.3s".
// Negative values are coerced to 0 — predictions can't be negative.
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds == null || !isFinite(seconds)) return "—";
  const s = Math.max(0, seconds);
  if (s < 1) return `${s.toFixed(1)}s`;
  const total = Math.round(s);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const sec = total % 60;
  if (h > 0) return `${h}h ${String(m).padStart(2, "0")}m ${String(sec).padStart(2, "0")}s`;
  if (m > 0) return `${m}m ${String(sec).padStart(2, "0")}s`;
  return `${sec}s`;
}

// formatPercent → "12.3%" / "—" for null. Caller already multiplies by 100
// if needed; this just adds the unit and clamps digits.
export function formatPercent(value: number | null | undefined, digits = 1): string {
  if (value == null || !isFinite(value)) return "—";
  return `${value.toFixed(digits)}%`;
}

// formatRelativeTime → "3s ago" / "5m ago" / "2h ago" / "3d ago".
// Tooltips on event timestamps use this; the absolute time is still in
// the surrounding mono text so power users can copy it.
export function formatRelativeTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  if (isNaN(then)) return "—";
  const delta = Math.max(0, (Date.now() - then) / 1000);
  if (delta < 60) return `${Math.floor(delta)}s ago`;
  if (delta < 3600) return `${Math.floor(delta / 60)}m ago`;
  if (delta < 86400) return `${Math.floor(delta / 3600)}h ago`;
  return `${Math.floor(delta / 86400)}d ago`;
}

// formatSignedPercent → "+5.3%" / "−2.1%". For prediction error δ on the
// dashboard event rows.
export function formatSignedPercent(value: number | null | undefined, digits = 1): string {
  if (value == null || !isFinite(value)) return "—";
  const sign = value >= 0 ? "+" : "−";
  return `${sign}${Math.abs(value).toFixed(digits)}%`;
}
