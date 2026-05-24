-- +goose Up
-- +goose StatementBegin

-- Persistent log of (predicted, actual) pairs at webhook .completed time.
--
-- Why a new table instead of joining `jobs` + `predictions`:
--   - `predictions` is populated by the collector via /predict job_ids,
--     which runs minutes-to-hours AFTER the webhook delivered the
--     completed event. The "what user actually saw on the dashboard"
--     value at predict-time can differ from the collector's later
--     prediction (different model version, different calibration
--     state). This log captures the live view.
--   - `jobs` indexes by GitHub job_id, which we don't know at webhook
--     time. The webhook works at workflow_run level only.
--   - We want calibration math (raw vs calibrated, factor at the time)
--     persisted alongside, which neither `jobs` nor `predictions` carry.
--
-- INSERT on workflow_run.completed when both predicted_sec (remembered
-- from .requested) and actual_sec (workflow-level duration from payload)
-- are present. UPSERT on run_id so retried webhooks update the row
-- rather than duplicate it.
--
-- Retention: not enforced here. At ~50-200 rows/day this stays small
-- for years; if it ever matters, a partial index on created_at and a
-- nightly delete-where-older-than-N would suffice.
CREATE TABLE IF NOT EXISTS prediction_log (
    id                  BIGSERIAL PRIMARY KEY,
    run_id              BIGINT NOT NULL,
    repo                TEXT NOT NULL,
    workflow            TEXT,
    head_branch         TEXT,
    head_sha            TEXT,
    event               TEXT,
    conclusion          TEXT,
    predicted_sec       DOUBLE PRECISION,
    predicted_raw_sec   DOUBLE PRECISION,
    calibration_factor  DOUBLE PRECISION,
    actual_sec          DOUBLE PRECISION,
    delta_pct           DOUBLE PRECISION,
    model_id            BIGINT,
    model_algo          TEXT,
    completed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id)
);

-- Two indexes for the typical filters: by repo (sidebar pill) and by
-- recency (default sort on the history page). repo+completed_at composite
-- doubles as covering for the common "show last N for this repo" query.
CREATE INDEX IF NOT EXISTS idx_prediction_log_recent
  ON prediction_log(completed_at DESC);
CREATE INDEX IF NOT EXISTS idx_prediction_log_repo_recent
  ON prediction_log(repo, completed_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS prediction_log;
-- +goose StatementEnd
