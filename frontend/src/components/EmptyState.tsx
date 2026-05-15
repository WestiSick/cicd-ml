import type { ReactNode } from "react";

/* Empty state — two lines of text, one explicit next action. No illustration.
 * See plan §"7. Пустые состояния и онбординг". */
export function EmptyState({
  title,
  hint,
  action,
}: {
  title: string;
  hint?: string;
  action?: ReactNode;
}) {
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        textAlign: "center",
        padding: "var(--s-12) var(--s-6)",
        border: "1px dashed var(--border-subtle)",
        borderRadius: "var(--r-8)",
        background: "var(--bg-elevated)",
      }}
    >
      <div style={{ fontSize: "var(--fs-16)", color: "var(--text-primary)" }}>
        {title}
      </div>
      {hint && (
        <div
          style={{
            marginTop: "var(--s-2)",
            color: "var(--text-secondary)",
            fontSize: "var(--fs-13)",
            maxWidth: 440,
          }}
        >
          {hint}
        </div>
      )}
      {action && <div style={{ marginTop: "var(--s-4)" }}>{action}</div>}
    </div>
  );
}
