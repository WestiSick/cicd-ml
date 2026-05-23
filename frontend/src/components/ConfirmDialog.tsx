import { useEffect, useState } from "react";

import { Button } from "./Button";

/* ConfirmDialog — modal with optional "type the name to confirm" guard.
 *
 * Used for destructive actions: delete repo, delete model. The
 * `requireText` prop adds a text-input gate so the user can't bury
 * "Confirm" under a stray click. When omitted, only a regular two-button
 * dialog appears.
 *
 * Why hand-rolled rather than Radix Dialog: we deliberately use a thin
 * shim across the app to keep bundle size small; Radix would pull in
 * focus-trap, portal, etc. Single-user tooling doesn't need that. If we
 * ever ship multi-user this should switch to a proper a11y primitive.
 */
export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel,
  requireText,
  danger,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  title: string;
  message: string;
  confirmLabel: string;
  requireText?: string;
  danger?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const [typed, setTyped] = useState("");

  // Reset the typed field whenever the dialog opens — otherwise it would
  // retain the previous attempt's text across opens of different rows.
  useEffect(() => {
    if (open) setTyped("");
  }, [open]);

  // Escape to cancel, Cmd/Ctrl+Enter to confirm when unguarded.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onCancel();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  if (!open) return null;

  const confirmEnabled = requireText ? typed === requireText : true;

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.5)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 100,
      }}
      onClick={onCancel}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--bg-elevated)",
          border: "1px solid var(--border-strong)",
          borderRadius: "var(--r-8)",
          padding: "var(--s-6)",
          maxWidth: 460,
          width: "92%",
        }}
      >
        <h3 style={{ margin: 0, fontSize: "var(--fs-20)", fontWeight: 500 }}>{title}</h3>
        <p
          style={{
            margin: "var(--s-3) 0 var(--s-4)",
            color: "var(--text-secondary)",
            fontSize: "var(--fs-13)",
            lineHeight: 1.5,
          }}
        >
          {message}
        </p>

        {requireText && (
          <div style={{ marginBottom: "var(--s-4)" }}>
            <div
              className="caps"
              style={{ color: "var(--text-tertiary)", marginBottom: "var(--s-2)" }}
            >
              Type <span className="mono" style={{ color: "var(--text-secondary)" }}>{requireText}</span> to confirm
            </div>
            <input
              autoFocus
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              spellCheck={false}
              style={{
                width: "100%",
                height: 36,
                background: "var(--bg-base)",
                border: "1px solid var(--border-subtle)",
                borderRadius: "var(--r-6)",
                padding: "0 var(--s-3)",
                color: "var(--text-primary)",
                fontFamily: "var(--font-mono)",
                fontSize: "var(--fs-13)",
                outline: "none",
              }}
            />
          </div>
        )}

        <div style={{ display: "flex", justifyContent: "flex-end", gap: "var(--s-2)" }}>
          <Button variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
          <Button
            variant={danger ? "danger" : "primary"}
            onClick={onConfirm}
            disabled={!confirmEnabled}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
