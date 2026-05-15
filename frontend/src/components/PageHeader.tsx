import type { ReactNode } from "react";

/* Standardised page header — every page uses this so heading sizes,
 * spacing and action alignment stay consistent. Don't roll your own. */
export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
}) {
  return (
    <header
      style={{
        display: "flex",
        alignItems: "flex-end",
        justifyContent: "space-between",
        gap: "var(--s-4)",
        paddingBottom: "var(--s-4)",
        marginBottom: "var(--s-6)",
        borderBottom: "1px solid var(--border-subtle)",
      }}
    >
      <div>
        <h1
          style={{
            margin: 0,
            fontSize: "var(--fs-28)",
            fontWeight: 500,
            letterSpacing: "-0.01em",
            lineHeight: "var(--lh-tight)",
          }}
        >
          {title}
        </h1>
        {subtitle && (
          <p
            style={{
              margin: "var(--s-1) 0 0",
              color: "var(--text-secondary)",
              fontSize: "var(--fs-13)",
            }}
          >
            {subtitle}
          </p>
        )}
      </div>
      {actions && <div style={{ display: "flex", gap: "var(--s-2)" }}>{actions}</div>}
    </header>
  );
}
