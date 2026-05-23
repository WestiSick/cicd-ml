-- +goose Up
-- +goose StatementBegin

-- Per-(repo, workflow) calibration coefficients for the predict path.
--
-- Why a separate table (not a column on `repos`):
--   - Calibration is per-workflow inside a repo. The "Deploy via SSH"
--     workflow in `santehlavka` has a wildly different actual/predicted
--     ratio than "Build and lint" in the same repo, because deploy
--     length is dominated by docker pull / restart while build length
--     is dominated by compile time. One coefficient per repo would
--     average them and help neither.
--   - We update via EMA on every `workflow_run.completed`. A separate
--     table keeps the write path narrow and doesn't churn the `repos`
--     row (which is read on every dashboard render).
--
-- Semantics:
--   factor: multiplier applied to the model's raw prediction.
--           predicted_final = predicted_raw × factor
--           1.0 = model is unbiased on this slice.
--           > 1.0 = model under-predicts (real takes longer).
--           < 1.0 = model over-predicts (real is faster).
--   n_observations: how many completed runs have shaped this factor.
--                   Predict path treats `n < 3` as "not enough data,
--                   skip calibration" — a single outlier shouldn't
--                   wreck the multiplier.
--   last_*: the most recent observation, for diagnostics on the
--           /admin → Calibrations page.
--   updated_at: also for the admin page; rows that haven't been
--               touched in a long time may indicate a dead workflow.
CREATE TABLE IF NOT EXISTS repo_calibration (
    repo_id            BIGINT  NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    workflow_name      TEXT    NOT NULL,
    factor             DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    n_observations     INTEGER NOT NULL DEFAULT 0,
    last_actual_sec    DOUBLE PRECISION,
    last_predicted_sec DOUBLE PRECISION,
    last_ratio         DOUBLE PRECISION,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, workflow_name)
);

CREATE INDEX IF NOT EXISTS idx_repo_calibration_updated
  ON repo_calibration(updated_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS repo_calibration;
-- +goose StatementEnd
