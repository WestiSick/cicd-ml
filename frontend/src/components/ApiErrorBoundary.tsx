import { Component, type ErrorInfo, type ReactNode } from "react";

import { ApiError } from "@/api/client";

/* ApiErrorBoundary — last-resort catch for uncaught React errors.
 *
 * Most API errors come from useQuery/useMutation hooks and are surfaced via
 * toast or per-page error state. This boundary catches the rest — render
 * crashes, malformed JSON, code errors — so the whole app doesn't go blank.
 *
 * Why hand-rolled rather than react-error-boundary: a 60-line class
 * component keeps the dependency surface minimal, and the only thing we
 * need is the catch-and-display behaviour, not the reset hooks the
 * library provides.
 *
 * The Reload button is a hard reload (not React state reset) because by
 * the time we reach this boundary the app's state is likely already
 * unrecoverable. A fresh load is the safest recovery path. */
export class ApiErrorBoundary extends Component<
  { children: ReactNode },
  { error: Error | null }
> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Surface to the dev console — we don't ship to Sentry in this single-user tool.
    // eslint-disable-next-line no-console
    console.error("ApiErrorBoundary caught:", error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    const err = this.state.error;
    const isApi = err instanceof ApiError;
    return (
      <div
        style={{
          display: "grid",
          placeItems: "center",
          minHeight: "100vh",
          padding: "var(--s-6)",
        }}
      >
        <div
          style={{
            maxWidth: 540,
            padding: "var(--s-6)",
            background: "var(--bg-elevated)",
            border: "1px solid var(--border-strong)",
            borderRadius: "var(--r-8)",
          }}
        >
          <div
            className="caps"
            style={{ color: "var(--err)", marginBottom: "var(--s-3)", fontSize: "var(--fs-11)" }}
          >
            Something went wrong
          </div>
          <h2 style={{ margin: 0, fontSize: "var(--fs-20)", fontWeight: 500 }}>{err.message}</h2>
          {isApi && (err as ApiError).userAction && (
            <p
              style={{
                marginTop: "var(--s-3)",
                color: "var(--text-secondary)",
                fontSize: "var(--fs-13)",
                lineHeight: 1.5,
              }}
            >
              {(err as ApiError).userAction}
            </p>
          )}
          <div style={{ marginTop: "var(--s-4)", display: "flex", gap: "var(--s-2)" }}>
            <button
              onClick={() => window.location.reload()}
              style={{
                height: 34,
                padding: "0 14px",
                background: "var(--text-primary)",
                color: "var(--bg-base)",
                border: "none",
                borderRadius: "var(--r-6)",
                cursor: "pointer",
                fontFamily: "var(--font-mono)",
                fontSize: "var(--fs-13)",
              }}
            >
              Reload
            </button>
            <a
              href="/admin#system-health"
              style={{
                alignSelf: "center",
                color: "var(--text-secondary)",
                textDecoration: "none",
                fontSize: "var(--fs-13)",
              }}
            >
              Check system health →
            </a>
          </div>
        </div>
      </div>
    );
  }
}
