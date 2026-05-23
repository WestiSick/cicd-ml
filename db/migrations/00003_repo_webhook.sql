-- +goose Up
-- +goose StatementBegin

-- Track GitHub webhook installation per repo.
--
-- When the user adds a repo and a PAT with admin:repo_hook scope is
-- available, the api-gateway calls POST /repos/{owner}/{repo}/hooks on
-- GitHub to install a workflow_run webhook pointing at our public URL.
-- The result lands here:
--
--   webhook_id            — the hook id GitHub assigned (NULL if not installed)
--   webhook_url           — the callback URL we registered (for diagnostics)
--   webhook_installed_at  — timestamp of successful install/update
--   webhook_status        — one of:
--       'not_attempted'    — never tried (no PAT, or PUBLIC_API_BASE looks like localhost)
--       'installed'        — live and verified
--       'failed_no_access' — 403/404 from GitHub (user lacks admin:repo_hook
--                            on this repo — common for upstream OSS repos like vitejs/vite)
--       'failed_unreachable' — our PUBLIC_API_BASE is not publicly reachable
--       'failed_other'     — network error / unexpected response; webhook_error has details
--   webhook_error         — last error message, NULL on success
--
-- All columns nullable so existing rows migrate cleanly without backfill.
ALTER TABLE repos
  ADD COLUMN IF NOT EXISTS webhook_id           BIGINT,
  ADD COLUMN IF NOT EXISTS webhook_url          TEXT,
  ADD COLUMN IF NOT EXISTS webhook_installed_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS webhook_status       TEXT NOT NULL DEFAULT 'not_attempted',
  ADD COLUMN IF NOT EXISTS webhook_error        TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE repos
  DROP COLUMN IF EXISTS webhook_id,
  DROP COLUMN IF EXISTS webhook_url,
  DROP COLUMN IF EXISTS webhook_installed_at,
  DROP COLUMN IF EXISTS webhook_status,
  DROP COLUMN IF EXISTS webhook_error;
-- +goose StatementEnd
