import type { CSSProperties, ReactNode } from "react";

/* Flat card — 1px border, no shadows. Hover only nudges the border. */
export function Card({
  children,
  style,
  interactive,
}: {
  children: ReactNode;
  style?: CSSProperties;
  interactive?: boolean;
}) {
  return (
    <div
      style={{
        background: "var(--bg-elevated)",
        border: "1px solid var(--border-subtle)",
        borderRadius: "var(--r-8)",
        padding: "var(--s-4)",
        transition: "border-color var(--t-hover) var(--ease)",
        cursor: interactive ? "pointer" : undefined,
        ...style,
      }}
      onMouseEnter={
        interactive
          ? (e) => ((e.currentTarget.style.borderColor = "var(--border-strong)"))
          : undefined
      }
      onMouseLeave={
        interactive
          ? (e) => ((e.currentTarget.style.borderColor = "var(--border-subtle)"))
          : undefined
      }
    >
      {children}
    </div>
  );
}
