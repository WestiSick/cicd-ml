import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { ApiError } from "@/api/client";
import { installRepoWebhook, removeRepoWebhook, type WebhookStatus } from "@/api/repos";
import { useT } from "@/i18n";

/* WebhookBadge — status pill + install/remove action for one repo's
 * GitHub webhook.
 *
 * The four non-installed states each get a distinct colour so the user
 * can tell at a glance whether they have a fixable problem (no token →
 * yellow), an unfixable one (upstream repo, no admin access → grey), or
 * a setup error (PUBLIC_API_BASE wrong → red).
 *
 * Action button:
 *   - When status=installed  → "Remove webhook" (with confirm via toast).
 *   - When status=any failure → "Install webhook" — retries the install
 *     using the persisted PAT.
 *   - When status=not_attempted → "Install webhook" (initial).
 *
 * The install path is fire-and-forget on the backend (goroutine writes
 * the result), but the manual install endpoint is synchronous so we get
 * the new status back in the response and update the cache immediately.
 */
type Props = {
  repoID: number;
  status: WebhookStatus;
  webhookURL?: string;
  errorMessage?: string;
};

export function WebhookBadge({ repoID, status, webhookURL, errorMessage }: Props) {
  const t = useT();
  const qc = useQueryClient();

  const install = useMutation({
    mutationFn: () => installRepoWebhook(repoID),
    onSuccess: (r) => {
      if (r.status === "installed") {
        toast.success(t("datasets.webhook.toast.installed"));
      } else {
        toast.error(t("datasets.webhook.toast.failed"), {
          description: r.error || t(`datasets.webhook.${r.status}`),
        });
      }
      qc.invalidateQueries({ queryKey: ["repos"] });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error(t("datasets.webhook.toast.failed"));
    },
  });

  const remove = useMutation({
    mutationFn: () => removeRepoWebhook(repoID),
    onSuccess: () => {
      toast.success(t("datasets.webhook.toast.removed"));
      qc.invalidateQueries({ queryKey: ["repos"] });
    },
    onError: (err: unknown) => {
      if (err instanceof ApiError) toast.error(err.message, { description: err.userAction });
      else toast.error("remove failed");
    },
  });

  const palette = STATUS_PALETTE[status];
  const labelKey =
    status === "installed" ? "datasets.webhook.installed"
    : status === "failed_no_access" ? "datasets.webhook.failed_no_access"
    : status === "failed_unreachable" ? "datasets.webhook.failed_unreachable"
    : status === "failed_other" ? "datasets.webhook.failed_other"
    : "datasets.webhook.not_attempted";

  const tooltip =
    status === "installed" && webhookURL
      ? t("datasets.webhook.tooltip_installed", { url: webhookURL })
      : errorMessage || "";

  return (
    <div style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
      <span
        title={tooltip}
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 4,
          padding: "2px 8px",
          borderRadius: "var(--r-pill)",
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          fontWeight: 500,
          textTransform: "uppercase",
          letterSpacing: "0.06em",
          color: palette.fg,
          background: palette.bg,
          border: palette.border ? `1px solid ${palette.border}` : "none",
          cursor: tooltip ? "help" : "default",
        }}
      >
        <Dot color={palette.fg} />
        {t(labelKey)}
      </span>
      {status === "installed" ? (
        <button
          onClick={() => remove.mutate()}
          disabled={remove.isPending}
          style={btnStyle}
          title={t("datasets.webhook.remove")}
        >
          ✕
        </button>
      ) : (
        <button
          onClick={() => install.mutate()}
          disabled={install.isPending}
          style={btnStyle}
        >
          {install.isPending ? "…" : t("datasets.webhook.install")}
        </button>
      )}
    </div>
  );
}

function Dot({ color }: { color: string }) {
  return (
    <span
      aria-hidden
      style={{
        display: "inline-block",
        width: 6, height: 6, borderRadius: "50%",
        background: color,
      }}
    />
  );
}

// One palette entry per status — green for live, grey for unfixable
// (no admin access on upstream OSS), yellow for fixable (no PAT or
// public URL), red for hard error.
const STATUS_PALETTE: Record<WebhookStatus, { fg: string; bg: string; border?: string }> = {
  installed:          { fg: "var(--ok)",            bg: "var(--ok-soft)" },
  not_attempted:      { fg: "var(--text-tertiary)", bg: "transparent", border: "var(--border-subtle)" },
  failed_no_access:   { fg: "var(--text-tertiary)", bg: "var(--bg-overlay)" },
  failed_unreachable: { fg: "var(--warn)",          bg: "var(--warn-soft)" },
  failed_other:       { fg: "var(--err)",           bg: "var(--err-soft)" },
};

const btnStyle: React.CSSProperties = {
  height: 22,
  padding: "0 8px",
  background: "transparent",
  color: "var(--text-secondary)",
  border: "1px solid var(--border-subtle)",
  borderRadius: "var(--r-6)",
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  cursor: "pointer",
};
